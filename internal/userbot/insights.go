package userbot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// ---- Va'dalarni kuzatish ----

// followupDelay — "keyinroq yozaman"dan keyin shuncha vaqt o'tib egasi yozmagan bo'lsa, eslatiladi.
const followupDelay = 3 * time.Hour

// promisePatterns — kotib javobidagi "keyinroq yozaman" ma'nosidagi iboralar (uz/ru/en).
var promisePatterns = []string{
	"keyinroq yoz", "keyin yoz", "o'zim yoz", "aniqlab yoz", "qaytib yoz", "bo'shasam yoz",
	"keyinroq javob", "keyin javob", "keyinroq aytaman", "keyin aytaman", "aniqlab aytaman",
	"tekshirib aytaman", "keyinroq aloqa", "keyinroq qo'ng'iroq", "keyin qo'ng'iroq",
	"позже напишу", "напишу позже", "потом напишу", "напишу потом", "отпишу", "перезвоню", "уточню и напишу",
	"get back to you", "text you later", "write later", "reply later", "message you later", "call you later",
}

func normalizeText(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer("’", "'", "‘", "'", "ʻ", "'", "ʼ", "'", "`", "'").Replace(s)
}

// IsPromise — javobda egasi keyinroq yozishga va'da berganmi.
func IsPromise(text string) bool {
	t := normalizeText(text)
	for _, p := range promisePatterns {
		if strings.Contains(t, p) {
			return true
		}
	}
	return false
}

func textHash(s string) string {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(normalizeText(s)), " ")))
	return hex.EncodeToString(sum[:16])
}

// trackPromise — yuborilgan javob va'da bo'lsa, eslatmani rejalashtiradi (matn saqlanmaydi, faqat xesh).
func (p *Pipeline) trackPromise(ctx context.Context, chatID int64, reply string) {
	if !IsPromise(reply) {
		return
	}
	if st, err := p.Store.GetSettings(ctx, p.Owner); err != nil || st.NoFollowups {
		return
	}
	if err := p.Store.SaveFollowup(ctx, store.Followup{
		OwnerID: p.Owner, ChatID: chatID, Hash: textHash(reply), DueAt: p.Now().Add(followupDelay),
	}); err != nil {
		p.Log.Warn("followup", "owner", p.Owner, "err", err)
	}
}

// contactName — kontakt ismi (Telegram kontaktlaridan), topilmasa "kontakt".
func (m *Manager) contactName(ctx context.Context, owner, chatID int64) string {
	if cs, err := m.Contacts(ctx, owner); err == nil {
		for _, c := range cs {
			if c.ID == chatID {
				return c.Name
			}
		}
	}
	return "kontakt"
}

// CheckFollowups muddati kelgan va'dalarni tekshiradi: chatdagi egasining oxirgi xabari hali ham
// kotibning "keyinroq yozaman" javobi bo'lsa — egasiga eslatadi, aks holda (o'zi yozgan) yopadi.
func (m *Manager) CheckFollowups(ctx context.Context, now time.Time) {
	m.init(ctx)
	due, err := m.Store.ListDueFollowups(ctx, now, 100)
	if err != nil {
		m.Log.Warn("followups", "err", err)
		return
	}
	for _, f := range due {
		m.mu.Lock()
		w := m.workers[f.OwnerID]
		m.mu.Unlock()
		var tg Telegram
		if w != nil {
			tg = w.telegram()
		}
		if tg == nil {
			// Sessiya ulanmagan: bir kun kutamiz, keyin tekshirib bo'lmagani uchun yopamiz.
			if now.Sub(f.DueAt) > 24*time.Hour {
				_ = m.Store.SetFollowupStatus(ctx, f.OwnerID, f.ChatID, store.FollowupDone)
			}
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		hist, err := tg.History(cctx, f.ChatID, 10)
		cancel()
		if err != nil {
			continue // keyingi safar
		}
		lastOwner := ""
		for i := len(hist) - 1; i >= 0; i-- {
			if hist[i].FromOwner {
				lastOwner = hist[i].Text
				break
			}
		}
		if lastOwner == "" || textHash(lastOwner) != f.Hash {
			_ = m.Store.SetFollowupStatus(ctx, f.OwnerID, f.ChatID, store.FollowupDone) // egasi o'zi yozgan
			continue
		}
		_ = m.Store.SetFollowupStatus(ctx, f.OwnerID, f.ChatID, store.FollowupReminded)
		m.Store.Audit(ctx, f.OwnerID, "followup_reminded", "")
		if m.Notify != nil {
			name := m.contactName(ctx, f.OwnerID, f.ChatID)
			m.Notify(ctx, f.OwnerID, fmt.Sprintf("⏰ Kotib %s ga «keyinroq yozaman» degan edi — siz hali yozmadingiz.", name), false)
		}
	}
}

// ---- Kechki xulosa ----

// digestHour — kechki xulosa yuboriladigan soat (egasining vaqt zonasi bo'yicha).
const digestHour = 21

// SendDigests egasining mahalliy vaqti bilan soat 21:00–21:59 oralig'ida kunlik xulosani
// bir marta yuboradi (audit "digest_sent" bo'yicha takrorlanmaydi). Faoliyat bo'lmasa — yubormaydi.
func (m *Manager) SendDigests(ctx context.Context, now time.Time) {
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
		if err != nil || st.NoDigest {
			continue
		}
		loc := st.Location()
		local := now.In(loc)
		if local.Hour() != digestHour {
			continue
		}
		if m.Entitled != nil && !m.Entitled(ctx, owner) {
			continue
		}
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
		if n, err := m.Store.CountAudit(ctx, owner, []string{"digest_sent"}, day); err != nil || n > 0 {
			continue
		}
		text, ok := m.digestText(ctx, owner, &st, now, day)
		if !ok {
			continue
		}
		m.Store.Audit(ctx, owner, "digest_sent", "")
		m.Notify(ctx, owner, text, true)
	}
}

func (m *Manager) digestText(ctx context.Context, owner int64, st *config.Settings, now, day time.Time) (string, bool) {
	prepared, _ := m.Store.CountAudit(ctx, owner, []string{"draft_created", "auto_sent"}, day)
	sentToday, _ := m.Store.CountAudit(ctx, owner, store.SentEvents, day)
	drafts, _ := m.Store.ListDrafts(ctx, owner, store.DraftPending, 100)
	open, _ := m.Store.ListOpenFollowups(ctx, owner)
	if prepared == 0 && len(drafts) == 0 && len(open) == 0 {
		return "", false
	}
	var b strings.Builder
	b.WriteString("🌙 Bugungi xulosa\n\n")
	fmt.Fprintf(&b, "✍️ Tayyorlangan javoblar: %d\n", prepared)
	fmt.Fprintf(&b, "📤 Yuborilgan: %d\n", sentToday)
	if len(drafts) > 0 {
		fmt.Fprintf(&b, "📥 Tasdiqingizni kutayotgan qoralamalar: %d\n", len(drafts))
	}
	if len(open) > 0 {
		names := make([]string, 0, len(open))
		seen := map[int64]bool{}
		for _, f := range open {
			if seen[f.ChatID] || len(names) >= 5 {
				continue
			}
			seen[f.ChatID] = true
			names = append(names, m.contactName(ctx, owner, f.ChatID))
		}
		fmt.Fprintf(&b, "⏰ «Keyinroq yozaman» deyilgan: %s\n", strings.Join(names, ", "))
	}
	if n, saved, err := store.ReplyStats(ctx, m.Store, owner, store.MonthStart(now, st.Location())); err == nil && n > 0 {
		fmt.Fprintf(&b, "\n⏱ Bu oy kotib %d ta javob yubordi — taxminan %s vaqtingizni tejadi.", n, store.FormatSaved(saved))
	}
	return b.String(), true
}
