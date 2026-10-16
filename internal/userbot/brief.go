package userbot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

const (
	// summaryChats — AI xulosaga kiradigan chatlar (eng oxirgi faollari).
	summaryChats = 15
	// summaryHistory — har chatdan o'qiladigan xabarlar.
	summaryHistory = 40
	// morningHour — ertalabki brifing soati (egasining vaqti).
	morningHour = 8
	// minUnanswered — eslatmaning eng qisqa muddati (UnansweredHours 1 bo'lsa ham).
	minUnanswered = time.Hour
)

// openChats — since'dan beri kontakt yozgan, kotibga ochiq (private emas) chatlar.
func (m *Manager) openChats(ctx context.Context, owner int64, since time.Time) []store.ChatActivity {
	all, err := m.Store.ListChats(ctx, owner, since)
	if err != nil {
		return nil
	}
	var out []store.ChatActivity
	for _, a := range all {
		if !a.Private && !a.LastIn.Before(since) {
			out = append(out, a)
		}
	}
	return out
}

func unanswered(chats []store.ChatActivity) []store.ChatActivity {
	var out []store.ChatActivity
	for _, a := range chats {
		if a.Unanswered() {
			out = append(out, a)
		}
	}
	return out
}

func (m *Manager) chatName(ctx context.Context, owner int64, a store.ChatActivity) string {
	if n := strings.TrimSpace(a.Name); n != "" {
		return n
	}
	return m.contactName(ctx, owner, a.ChatID)
}

// waitingList — "Aziz (14:20 dan), Vali (09:05 dan)" ko'rinishida, ko'pi bilan 5 ta.
func waitingList(ctx context.Context, m *Manager, owner int64, chats []store.ChatActivity, loc *time.Location) string {
	parts := make([]string, 0, 5)
	for i, a := range chats {
		if i >= 5 {
			parts = append(parts, fmt.Sprintf("+%d", len(chats)-5))
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%s dan)", m.chatName(ctx, owner, a), a.LastIn.In(loc).Format("15:04")))
	}
	return strings.Join(parts, ", ")
}

// aiSummary — chatlarning since'dan keyingi yozishmalari AI xulosasi. Sessiya ulanmagan
// yoki model xato qilsa — bo'sh (xulosa AI qismisiz yuboriladi).
func (m *Manager) aiSummary(ctx context.Context, owner int64, st *config.Settings, chats []store.ChatActivity, since time.Time, period string) string {
	an, ok := analystOf(m.LLM)
	tg, _ := m.live(owner)
	if !ok || tg == nil {
		return ""
	}
	var logs []llm.ChatLog
	for _, a := range chats {
		if len(logs) >= summaryChats {
			break
		}
		hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		hist, err := tg.History(hctx, a.ChatID, summaryHistory)
		cancel()
		if err != nil {
			continue
		}
		var turns []llm.Turn
		fromContact := false
		for _, t := range hist {
			if t.At.IsZero() || !t.At.Before(since) {
				turns = append(turns, t)
				fromContact = fromContact || !t.FromOwner
			}
		}
		if fromContact {
			logs = append(logs, llm.ChatLog{Name: m.chatName(ctx, owner, a), Turns: turns})
		}
	}
	if len(logs) == 0 {
		return ""
	}
	sctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, err := an.Summarize(sctx, llm.SummaryRequest{OwnerName: st.OwnerName, Period: period, Chats: logs})
	if err != nil {
		m.Store.Audit(ctx, owner, "llm_error", "summary")
		m.Log.Warn("summary", "owner", owner, "err", err)
		return ""
	}
	m.Store.Audit(ctx, owner, "summary_made", fmt.Sprintf("chats=%d", len(logs)))
	return out
}

// SendMorningBriefs — MorningBrief yoqilgan egalarga soat 8:00 da bir marta: tunda kim yozdi,
// kutayotgan qoralamalar, bugungi taqvim va (SmartDigest bo'lsa) tungi xabarlar AI xulosasi.
// Aytadigan hech narsa bo'lmasa yuborilmaydi.
func (m *Manager) SendMorningBriefs(ctx context.Context, now time.Time) {
	m.init(ctx)
	if m.Notify == nil {
		return
	}
	owners, err := m.Store.ListActiveOwners(ctx)
	if err != nil {
		return
	}
	for _, owner := range owners {
		st, err := m.Store.GetSettings(ctx, owner)
		if err != nil || !st.MorningBrief {
			continue
		}
		loc := st.Location()
		local := now.In(loc)
		if local.Hour() != morningHour || (m.Entitled != nil && !m.Entitled(ctx, owner)) {
			continue
		}
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
		if n, err := m.Store.CountAudit(ctx, owner, []string{"morning_sent"}, day); err != nil || n > 0 {
			continue
		}
		text, ok := m.morningText(ctx, owner, &st, day)
		if !ok {
			continue
		}
		m.Store.Audit(ctx, owner, "morning_sent", "")
		m.Notify(ctx, owner, text, true)
	}
}

func (m *Manager) morningText(ctx context.Context, owner int64, st *config.Settings, day time.Time) (string, bool) {
	since := day.Add(-(24 - digestHour) * time.Hour) // kechagi kechki xulosadan beri
	loc := st.Location()
	chats := m.openChats(ctx, owner, since)
	drafts, _ := m.Store.ListDrafts(ctx, owner, store.DraftPending, 100)
	pro := m.pro(ctx, owner)
	var busy []llm.Interval
	if pro && m.Google != nil && m.Google.Connected(ctx, owner) {
		gctx, cancel := context.WithTimeout(ctx, googleTimeout)
		busy, _ = m.Google.Busy(gctx, owner, day, day.Add(24*time.Hour))
		cancel()
	}
	summary := ""
	if st.SmartDigest && pro && len(chats) > 0 {
		summary = m.aiSummary(ctx, owner, st, chats, since, "tunda")
	}
	if len(chats) == 0 && len(drafts) == 0 && len(busy) == 0 {
		return "", false
	}
	var b strings.Builder
	b.WriteString("☀️ Xayrli tong!\n\n")
	if len(chats) > 0 {
		names := make([]string, 0, 5)
		for i, a := range chats {
			if i >= 5 {
				names = append(names, fmt.Sprintf("+%d", len(chats)-5))
				break
			}
			names = append(names, m.chatName(ctx, owner, a))
		}
		fmt.Fprintf(&b, "📨 Kechadan beri yozganlar: %s\n", strings.Join(names, ", "))
	}
	if w := unanswered(chats); len(w) > 0 {
		fmt.Fprintf(&b, "❗ Javob kutayotganlar: %d\n", len(w))
	}
	if len(drafts) > 0 {
		fmt.Fprintf(&b, "📥 Tasdiqingizni kutayotgan qoralamalar: %d\n", len(drafts))
	}
	if len(busy) > 0 {
		slots := make([]string, 0, len(busy))
		for i, iv := range busy {
			if i >= 8 {
				break
			}
			slots = append(slots, iv.Start.In(loc).Format("15:04")+"–"+iv.End.In(loc).Format("15:04"))
		}
		fmt.Fprintf(&b, "📅 Bugun taqvimda band: %s\n", strings.Join(slots, ", "))
	}
	if summary != "" {
		b.WriteString("\n📝 Tunda nima bo'ldi:\n" + summary + "\n")
	}
	return strings.TrimRight(b.String(), "\n"), true
}

// CheckUnanswered — kontakt yozganiga UnansweredHours (default 4) soat bo'lib javob berilmagan
// chatlar haqida egasiga eslatadi: har yangi xabar uchun bir marta, tinch soatlarda emas.
// Qoralamasi kutayotgan chatlar eslatilmaydi (u haqda allaqachon bildirilgan).
func (m *Manager) CheckUnanswered(ctx context.Context, now time.Time) {
	m.init(ctx)
	if m.Notify == nil {
		return
	}
	list, err := m.Store.ListUnanswered(ctx, now.Add(-minUnanswered), 500)
	if err != nil {
		m.Log.Warn("unanswered", "err", err)
		return
	}
	byOwner := map[int64][]store.ChatActivity{}
	var order []int64
	for _, a := range list {
		if _, ok := byOwner[a.OwnerID]; !ok {
			order = append(order, a.OwnerID)
		}
		byOwner[a.OwnerID] = append(byOwner[a.OwnerID], a)
	}
	for _, owner := range order {
		st, err := m.Store.GetSettings(ctx, owner)
		if err != nil {
			continue
		}
		wait := st.Unanswered()
		if wait == 0 || st.Mode == config.ModeOff || st.InQuiet(now) || (m.Entitled != nil && !m.Entitled(ctx, owner)) {
			continue
		}
		drafts, _ := m.Store.ListDrafts(ctx, owner, store.DraftPending, 100)
		hasDraft := map[int64]bool{}
		for _, d := range drafts {
			hasDraft[d.ChatID] = true
		}
		var due []store.ChatActivity
		for _, a := range byOwner[owner] {
			if now.Sub(a.LastIn) < max(wait, minUnanswered) {
				continue
			}
			_ = m.Store.MarkChatReminded(ctx, owner, a.ChatID, now)
			if !hasDraft[a.ChatID] {
				due = append(due, a)
			}
		}
		if len(due) == 0 {
			continue
		}
		parts := make([]string, 0, 5)
		for i, a := range due {
			if i >= 5 {
				parts = append(parts, fmt.Sprintf("yana %d ta", len(due)-5))
				break
			}
			parts = append(parts, fmt.Sprintf("%s (%s)", m.chatName(ctx, owner, a), ago(now.Sub(a.LastIn))))
		}
		m.Store.Audit(ctx, owner, "unanswered_reminded", fmt.Sprintf("n=%d", len(due)))
		m.Notify(ctx, owner, "⏳ Javob kutyapti: "+strings.Join(parts, ", "), false)
	}
}

// ago — "4 soatdan beri", "35 daqiqadan beri".
func ago(d time.Duration) string {
	if d >= time.Hour {
		return fmt.Sprintf("%d soatdan beri", int(d/time.Hour))
	}
	return fmt.Sprintf("%d daqiqadan beri", max(1, int(d/time.Minute)))
}
