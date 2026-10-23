package miniapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// Admin paneli (faqat ADMIN_IDS): foydalanuvchilar, xavfsizlik rejimi, foydalanish va xavf
// belgilari, sovg'a obuna, bloklash, xabar va to'lovlar. Xabar matnlari hech qachon ko'rsatilmaydi —
// faqat audit hodisalari soni (matnsiz meta).

const (
	adminWindow    = 7 * 24 * time.Hour // ro'yxat va xavf baholash oynasi
	adminPageSize  = 100
	maxBanReason   = 200
	maxAdminText   = 2000
	broadcastPause = 50 * time.Millisecond // Telegram bot limiti (~30 xabar/s) ichida
)

// Xavfsizlik darajasi (sozlamalar bo'yicha) va foydalanish belgisi (hodisalar bo'yicha).
const (
	safetySafe   = "safe"
	safetyMedium = "medium"
	safetyHigh   = "high"

	signalOK         = "ok"
	signalWatch      = "watch"
	signalSuspicious = "suspicious"
	signalIdle       = "idle"
)

// assessment — bitta foydalanuvchining baholanishi (ro'yxat va tafsilot uchun).
type assessment struct {
	Mode        string   `json:"mode"`   // off | draft | auto | shadow
	Safety      string   `json:"safety"` // safe | medium | high
	SafetyNotes []string `json:"safety_notes"`
	Signal      string   `json:"signal"` // ok | watch | suspicious | idle
	SignalNotes []string `json:"signal_notes"`
	Prepared    int      `json:"prepared"` // tayyorlangan javoblar (qoralama + avtomatik)
	Sent        int      `json:"sent"`     // yuborilgan (avtomatik + tasdiqlangan)
	Approval    int      `json:"approval"` // tasdiqlangan qoralamalar ulushi, %; -1 — ma'lumot yo'q
}

// assess sozlama va hodisalar sonidan (oxirgi 7 kun) xavfsizlik va foydalanish bahosini chiqaradi.
// Chegaralar taxminiy: maqsad — admin e'tiborini kerakli akkauntga qaratish, avtomatik jazo emas.
func assess(st config.Settings, c map[string]int, now time.Time) assessment {
	a := assessment{Mode: string(st.Mode), Safety: safetySafe, SafetyNotes: []string{}, SignalNotes: []string{}, Approval: -1}
	if st.ShadowActive(now) {
		a.Mode = "shadow"
	}
	auto := st.Mode == config.ModeAuto && a.Mode != "shadow"
	autoRules := 0
	for _, r := range st.Rules {
		if !r.Disabled && r.Mode != nil && *r.Mode == config.ModeAuto {
			autoRules++
		}
	}
	risky := 0
	if auto {
		a.SafetyNotes = append(a.SafetyNotes, "Avtomatik rejim")
	}
	if autoRules > 0 {
		a.SafetyNotes = append(a.SafetyNotes, fmt.Sprintf("Avtomatik qoidalar: %d", autoRules))
	}
	for _, f := range []struct {
		on   bool
		note string
	}{
		{st.ReplyUnknown, "Notanishlarga javob"},
		{st.ReplyBots, "Botlarga javob"},
		{st.ReplyGroups, "Guruhlarga javob"},
		{st.AllowLinks, "Havolalarga ruxsat"},
	} {
		if f.on {
			risky++
			a.SafetyNotes = append(a.SafetyNotes, f.note)
		}
	}
	switch {
	case (auto || autoRules > 0) && risky > 0:
		a.Safety = safetyHigh
	case auto || autoRules > 0 || risky > 0:
		a.Safety = safetyMedium
	}

	a.Prepared = c["draft_created"] + c["auto_sent"]
	a.Sent = c["auto_sent"] + c["draft_sent"]
	if decided := c["draft_sent"] + c["draft_rejected"]; decided > 0 {
		a.Approval = c["draft_sent"] * 100 / decided
	}
	note := func(n int, text string) {
		if n > 0 {
			a.SignalNotes = append(a.SignalNotes, fmt.Sprintf("%s: %d", text, n))
		}
	}
	note(c["flood_wait"], "Telegram cheklovi (flood)")
	note(c["circuit_breaker"], "Favqulodda to'xtash")
	note(c["fatal"], "Sessiya xatosi")
	note(c["output_rejected"], "Filtr rad etgan javoblar")
	note(c["blocked"], "Xavfli kiruvchi xabarlar")
	note(c["llm_error"], "AI xatolari")
	switch {
	case c["flood_wait"] > 0 || c["circuit_breaker"] > 0 || c["output_rejected"] >= 10 || c["auto_sent"] >= 700:
		a.Signal = signalSuspicious
	case c["output_rejected"] >= 3 || c["blocked"] >= 10 || c["auto_sent"] >= 300 || c["fatal"] > 0:
		a.Signal = signalWatch
	case a.Prepared == 0:
		a.Signal = signalIdle
	default:
		a.Signal = signalOK
	}
	if c["auto_sent"] >= 300 {
		a.SignalNotes = append(a.SignalNotes, fmt.Sprintf("Ko'p avtomatik xabar: %d / 7 kun", c["auto_sent"]))
	}
	return a
}

// adminUser — ro'yxatdagi qator.
type adminUser struct {
	ID        int64               `json:"id"`
	Name      string              `json:"name"`
	Username  string              `json:"username"`
	OwnerName string              `json:"owner_name"`
	Status    store.AccountStatus `json:"status"`
	Note      string              `json:"note"`
	Consented bool                `json:"consented"`
	CreatedAt time.Time           `json:"created_at"`
	SeenAt    *time.Time          `json:"seen_at"`
	LastEvent *time.Time          `json:"last_event"`
	Admin     bool                `json:"admin"`
	Banned    bool                `json:"banned"`
	BanReason string              `json:"ban_reason,omitempty"`
	Billing   *billing.Status     `json:"billing,omitempty"`
	assessment
}

func (s *Server) adminRow(ctx context.Context, r store.AccountRow, now time.Time, withBilling bool) adminUser {
	u := adminUser{
		ID: r.OwnerID, Name: r.Name, Username: r.Username, OwnerName: r.Settings.OwnerName,
		Status: r.Status, Note: r.StatusNote, Consented: r.ConsentAt != nil, CreatedAt: r.CreatedAt,
		SeenAt: r.SeenAt, LastEvent: r.LastEventAt, Admin: s.Admins[r.OwnerID],
		assessment: assess(r.Settings, r.Counts, now),
	}
	if r.Ban != nil {
		u.Banned, u.BanReason = true, r.Ban.Reason
	}
	if withBilling && s.Billing != nil {
		if bs, err := s.Billing.Status(ctx, r.OwnerID); err == nil {
			u.Billing = &bs
		}
	}
	return u
}

// authAdmin — faqat ADMIN_IDS (asosiy akkaunt). Boshqalarga 404: panel borligi ham bilinmasin.
func (s *Server) authAdmin(next func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return s.authMain(func(w http.ResponseWriter, r *http.Request, owner int64) {
		if !s.Admins[owner] {
			httpErr(w, http.StatusNotFound, "topilmadi")
			return
		}
		next(w, r, owner)
	})
}

func (s *Server) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/overview", s.authAdmin(s.handleAdminOverview))
	mux.HandleFunc("GET /api/admin/users", s.authAdmin(s.handleAdminUsers))
	mux.HandleFunc("GET /api/admin/users/{id}", s.authAdmin(s.handleAdminUser))
	mux.HandleFunc("POST /api/admin/users/{id}/grant", s.authAdmin(s.handleAdminGrant))
	mux.HandleFunc("POST /api/admin/users/{id}/revoke-grant", s.authAdmin(s.handleAdminRevokeGrant))
	mux.HandleFunc("POST /api/admin/users/{id}/ban", s.authAdmin(s.handleAdminBan))
	mux.HandleFunc("DELETE /api/admin/users/{id}/ban", s.authAdmin(s.handleAdminUnban))
	mux.HandleFunc("POST /api/admin/users/{id}/pause", s.authAdmin(s.handleAdminPause))
	mux.HandleFunc("POST /api/admin/users/{id}/message", s.authAdmin(s.handleAdminMessage))
	mux.HandleFunc("GET /api/admin/payments", s.authAdmin(s.handleAdminPayments))
	mux.HandleFunc("POST /api/admin/refund", s.authAdmin(s.handleAdminRefund))
	mux.HandleFunc("POST /api/admin/broadcast", s.authAdmin(s.handleAdminBroadcast))
	mux.HandleFunc("POST /api/admin/charity/{id}/paid", s.authAdmin(s.handleAdminCharityPaid))
	mux.HandleFunc("POST /api/admin/charity/manual", s.authAdmin(s.handleAdminCharityManual))
}

func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request, _ int64) {
	now := time.Now()
	rows, err := s.Store.ListAccounts(r.Context(), now.Add(-adminWindow), 0)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	type counter map[string]int
	out := struct {
		Users     int            `json:"users"`
		New7d     int            `json:"new_7d"`
		Seen7d    int            `json:"seen_7d"`
		Consented int            `json:"consented"`
		Banned    int            `json:"banned"`
		Status    counter        `json:"status"`
		Modes     counter        `json:"modes"`
		Safety    counter        `json:"safety"`
		Signals   counter        `json:"signals"`
		Events    counter        `json:"events"` // oxirgi 7 kun, barcha foydalanuvchilar
		Billing   *billingTotals `json:"billing"`
	}{Status: counter{}, Modes: counter{}, Safety: counter{}, Signals: counter{}, Events: counter{}}
	week := now.Add(-adminWindow)
	for _, row := range rows {
		a := assess(row.Settings, row.Counts, now)
		out.Users++
		out.Status[string(row.Status)]++
		if row.CreatedAt.After(week) {
			out.New7d++
		}
		if row.SeenAt != nil && row.SeenAt.After(week) {
			out.Seen7d++
		}
		if row.ConsentAt != nil {
			out.Consented++
		}
		if row.Ban != nil {
			out.Banned++
		}
		out.Modes[a.Mode]++
		out.Safety[a.Safety]++
		out.Signals[a.Signal]++
		for k, v := range row.Counts {
			out.Events[k] += v
		}
	}
	if st, err := s.Store.Stats(r.Context(), now); err == nil {
		out.Billing = &billingTotals{
			Enabled: s.Billing != nil && s.Billing.Enabled(), PaidActive: st.PaidActive,
			StarsTotal: st.StarsTotal, Referrals: st.Referrals, RefPaid: st.RefPaid,
		}
	}
	writeJSON(w, 200, out)
}

type billingTotals struct {
	Enabled    bool  `json:"enabled"`
	PaidActive int   `json:"paid_active"`
	StarsTotal int64 `json:"stars_total"`
	Referrals  int   `json:"referrals"`
	RefPaid    int   `json:"ref_paid"`
}

// handleAdminUsers — qidiruv (ism, @username, ID) va filtr: all | active | auto | risky | banned |
// paused | pending | paid | idle.
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request, _ int64) {
	now := time.Now()
	rows, err := s.Store.ListAccounts(r.Context(), now.Add(-adminWindow), 0)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	q := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(r.URL.Query().Get("q")), "@"))
	filter := r.URL.Query().Get("filter")
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	matched := make([]adminUser, 0, len(rows))
	for _, row := range rows {
		if q != "" && !strings.Contains(strings.ToLower(row.Name), q) && !strings.Contains(strings.ToLower(row.Username), q) &&
			!strings.Contains(strings.ToLower(row.Settings.OwnerName), q) && !strings.Contains(strconv.FormatInt(row.OwnerID, 10), q) {
			continue
		}
		u := s.adminRow(r.Context(), row, now, filter == "paid")
		if !adminFilter(u, filter) {
			continue
		}
		matched = append(matched, u)
	}
	if filter == "risky" {
		// Eng xavflilar yuqorida.
		rank := map[string]int{signalSuspicious: 0, signalWatch: 1}
		sort.SliceStable(matched, func(i, j int) bool {
			ri, ok := rank[matched[i].Signal]
			if !ok {
				ri = 2
			}
			rj, ok := rank[matched[j].Signal]
			if !ok {
				rj = 2
			}
			return ri < rj
		})
	}
	total := len(matched)
	offset = min(max(offset, 0), total)
	page := matched[offset:min(offset+adminPageSize, total)]
	if s.Billing != nil && filter != "paid" {
		for i := range page {
			if bs, err := s.Billing.Status(r.Context(), page[i].ID); err == nil {
				page[i].Billing = &bs
			}
		}
	}
	writeJSON(w, 200, map[string]any{"total": total, "offset": offset, "users": page})
}

func adminFilter(u adminUser, filter string) bool {
	switch filter {
	case "active":
		return u.Status == store.StatusActive
	case "paused":
		return u.Status == store.StatusPaused
	case "pending":
		return u.Status == store.StatusPending || !u.Consented
	case "auto":
		return u.Mode == string(config.ModeAuto)
	case "risky":
		return u.Safety == safetyHigh || u.Signal == signalSuspicious || u.Signal == signalWatch
	case "banned":
		return u.Banned
	case "idle":
		return u.Signal == signalIdle
	case "paid":
		return u.Billing != nil && (u.Billing.Paid || u.Billing.Bonus)
	}
	return true
}

func adminTarget(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpErr(w, 400, "id noto'g'ri")
		return 0, false
	}
	return id, true
}

func (s *Server) handleAdminUser(w http.ResponseWriter, r *http.Request, _ int64) {
	id, ok := adminTarget(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	acc, err := s.Store.GetAccount(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		httpErr(w, 404, "foydalanuvchi topilmadi")
		return
	} else if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	now := time.Now()
	st, _ := s.Store.GetSettings(ctx, id)
	row := store.AccountRow{Account: acc, Settings: st, Counts: countEvents(ctx, s.Store, id, now.Add(-adminWindow))}
	if b, err := s.Store.GetBan(ctx, id); err == nil {
		row.Ban = &b
	}
	audit, _ := s.Store.ListAudit(ctx, id, 50)
	if len(audit) > 0 {
		row.LastEventAt = &audit[0].At
	}
	u := s.adminRow(ctx, row, now, true)
	type entry struct {
		At     time.Time `json:"at"`
		Event  string    `json:"event"`
		Detail string    `json:"detail"`
	}
	events := make([]entry, 0, len(audit))
	for _, e := range audit {
		events = append(events, entry{e.At, e.Event, e.Detail})
	}
	links, _ := s.Store.ListLinks(ctx, id)
	schedules, _ := s.Store.ListSchedules(ctx, id)
	googleOn := false
	if s.Google != nil {
		googleOn, _ = s.Google.Status(ctx, id)
	}
	out := map[string]any{
		"user":     u,
		"counts":   row.Counts,
		"counts30": countEvents(ctx, s.Store, id, now.Add(-30*24*time.Hour)),
		"events":   events,
		"settings": map[string]any{
			"language": st.Language, "persona": st.DefaultPersona, "rules": len(st.Rules),
			"quiet": st.Quiet.Enabled, "learn": st.Learn, "knowledge": st.Knowledge != "", "about": st.About != "",
			"routine": len(st.Routine), "voice": st.ReplyVoice, "photos": st.ReplyPhotos, "drafts_in_bot": st.DraftsInBot,
		},
		"linked":    len(links),
		"schedules": len(schedules),
		"google":    googleOn,
	}
	if u.Banned {
		out["ban"] = map[string]any{"reason": row.Ban.Reason, "by": row.Ban.By, "at": row.Ban.At}
	}
	writeJSON(w, 200, out)
}

func countEvents(ctx context.Context, st store.Store, owner int64, since time.Time) map[string]int {
	out := map[string]int{}
	es, err := st.ListAuditSince(ctx, owner, store.AdminEvents, since, 100000)
	if err != nil {
		return out
	}
	for _, e := range es {
		out[e.Event]++
	}
	return out
}

func (s *Server) adminAudit(ctx context.Context, admin, target int64, event, detail string) {
	s.Store.Audit(ctx, target, event, strings.TrimSpace(fmt.Sprintf("admin=%d %s", admin, detail)))
	s.logger().Info("admin action", "admin", admin, "target", target, "event", event, "detail", detail)
}

func (s *Server) notifyUser(ctx context.Context, owner int64, text string) {
	if s.Notify != nil {
		s.Notify(context.WithoutCancel(ctx), owner, text, false)
	}
}

func (s *Server) handleAdminGrant(w http.ResponseWriter, r *http.Request, admin int64) {
	id, ok := adminTarget(w, r)
	if !ok {
		return
	}
	var in struct {
		Days   int    `json:"days"`
		Tier   string `json:"tier"`
		Notify bool   `json:"notify"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if s.Billing == nil {
		httpErr(w, 409, "to'lov tizimi yoqilmagan — xizmat hammaga bepul")
		return
	}
	if _, err := s.Store.GetAccount(r.Context(), id); err != nil {
		httpErr(w, 404, "foydalanuvchi topilmadi")
		return
	}
	tier := billing.Tier(in.Tier)
	if err := s.Billing.Grant(r.Context(), id, in.Days, tier); err != nil {
		if errors.Is(err, billing.ErrDisabled) {
			httpErr(w, 409, "to'lov tizimi yoqilmagan — xizmat hammaga bepul")
			return
		}
		httpErr(w, 400, err.Error())
		return
	}
	s.adminAudit(r.Context(), admin, id, "admin_grant", fmt.Sprintf("%s +%d", tier, in.Days))
	if in.Notify {
		s.notifyUser(r.Context(), id, fmt.Sprintf("🎁 Sizga %d kunlik %s obunasi sovg'a qilindi! Kotib cheklovsiz ishlaydi.", in.Days, tierTitle(tier)))
	}
	bs, _ := s.Billing.Status(r.Context(), id)
	writeJSON(w, 200, map[string]any{"ok": true, "billing": bs})
}

func tierTitle(t billing.Tier) string {
	if t == billing.TierPro {
		return "Pro"
	}
	return "Advanced"
}

func (s *Server) handleAdminRevokeGrant(w http.ResponseWriter, r *http.Request, admin int64) {
	id, ok := adminTarget(w, r)
	if !ok {
		return
	}
	if s.Billing == nil {
		httpErr(w, 409, "to'lov tizimi yoqilmagan")
		return
	}
	if err := s.Billing.RevokeGrant(r.Context(), id); err != nil {
		if errors.Is(err, billing.ErrNoSubscription) {
			httpErr(w, 409, "faol sovg'a yoki bonus yo'q")
			return
		}
		httpErr(w, 500, "xatolik")
		return
	}
	s.adminAudit(r.Context(), admin, id, "admin_revoke_grant", "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleAdminBan(w http.ResponseWriter, r *http.Request, admin int64) {
	id, ok := adminTarget(w, r)
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
		Notify bool   `json:"notify"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Reason = strings.Join(strings.Fields(in.Reason), " ")
	if utf8.RuneCountInString(in.Reason) > maxBanReason {
		httpErr(w, 400, "sabab juda uzun")
		return
	}
	if s.Admins[id] {
		httpErr(w, 400, "adminni bloklab bo'lmaydi")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if err := s.Store.SetBan(ctx, store.Ban{OwnerID: id, Reason: in.Reason, By: admin}); err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	// Kotib darhol to'xtaydi; qayta ishga tushganda ham (holat paused) ulanmaydi.
	s.Ctl.Pause(id)
	if acc, err := s.Store.GetAccount(ctx, id); err == nil && acc.Status == store.StatusActive {
		_ = s.Store.SetStatus(ctx, id, store.StatusPaused, "admin tomonidan bloklangan")
	}
	// Bloklangan foydalanuvchidan keyingi oy pul yechilmasin.
	if s.Billing != nil {
		if err := s.Billing.Cancel(ctx, id); err != nil && !errors.Is(err, billing.ErrNoSubscription) && !errors.Is(err, billing.ErrDisabled) {
			s.logger().Warn("cancel on ban", "owner", id, "err", err)
		}
	}
	s.adminAudit(ctx, admin, id, "admin_ban", in.Reason)
	if in.Notify {
		text := "⛔ Akkauntingiz administrator tomonidan bloklandi, kotib to'xtatildi."
		if in.Reason != "" {
			text += "\nSabab: " + in.Reason
		}
		s.notifyUser(ctx, id, text)
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleAdminUnban(w http.ResponseWriter, r *http.Request, admin int64) {
	id, ok := adminTarget(w, r)
	if !ok {
		return
	}
	if _, err := s.Store.GetBan(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		httpErr(w, 409, "foydalanuvchi bloklanmagan")
		return
	}
	if err := s.Store.DeleteBan(r.Context(), id); err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	s.adminAudit(r.Context(), admin, id, "admin_unban", "")
	s.notifyUser(r.Context(), id, "✅ Akkauntingizdan blok olib tashlandi. Kotibni ilovada qayta yoqishingiz mumkin.")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleAdminPause — kotibni vaqtincha to'xtatish (blokdan yengilroq: foydalanuvchi o'zi qayta yoqa oladi).
func (s *Server) handleAdminPause(w http.ResponseWriter, r *http.Request, admin int64) {
	id, ok := adminTarget(w, r)
	if !ok {
		return
	}
	acc, err := s.Store.GetAccount(r.Context(), id)
	if err != nil {
		httpErr(w, 404, "foydalanuvchi topilmadi")
		return
	}
	s.Ctl.Pause(id)
	if acc.Status == store.StatusActive {
		_ = s.Store.SetStatus(r.Context(), id, store.StatusPaused, "admin to'xtatdi")
	}
	s.adminAudit(r.Context(), admin, id, "admin_pause", "")
	s.notifyUser(r.Context(), id, "⏸ Kotibingiz administrator tomonidan vaqtincha to'xtatildi. Savollar bo'lsa, administrator bilan bog'laning.")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func readAdminText(w http.ResponseWriter, r *http.Request) (string, bool) {
	var in struct {
		Text string `json:"text"`
	}
	if !readJSON(w, r, &in) {
		return "", false
	}
	in.Text = strings.TrimSpace(in.Text)
	if in.Text == "" || utf8.RuneCountInString(in.Text) > maxAdminText {
		httpErr(w, 400, fmt.Sprintf("matn 1–%d belgi bo'lishi kerak", maxAdminText))
		return "", false
	}
	return in.Text, true
}

func (s *Server) handleAdminMessage(w http.ResponseWriter, r *http.Request, admin int64) {
	id, ok := adminTarget(w, r)
	if !ok {
		return
	}
	text, ok := readAdminText(w, r)
	if !ok {
		return
	}
	if s.Notify == nil {
		httpErr(w, 409, "bot ulanmagan")
		return
	}
	s.notifyUser(r.Context(), id, "📩 Administrator xabari:\n\n"+text)
	s.adminAudit(r.Context(), admin, id, "admin_message", "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleAdminBroadcast — rozilik bergan va bloklanmagan barcha foydalanuvchilarga xabar (fonda).
func (s *Server) handleAdminBroadcast(w http.ResponseWriter, r *http.Request, admin int64) {
	text, ok := readAdminText(w, r)
	if !ok {
		return
	}
	if s.Notify == nil {
		httpErr(w, 409, "bot ulanmagan")
		return
	}
	rows, err := s.Store.ListAccounts(r.Context(), time.Now(), 0)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	var ids []int64
	for _, row := range rows {
		if row.ConsentAt != nil && row.Ban == nil {
			ids = append(ids, row.OwnerID)
		}
	}
	s.logger().Info("admin broadcast", "admin", admin, "recipients", len(ids))
	ctx := context.WithoutCancel(r.Context())
	go func() {
		for _, id := range ids {
			s.Notify(ctx, id, "📢 "+text, false)
			time.Sleep(broadcastPause)
		}
	}()
	writeJSON(w, 200, map[string]int{"recipients": len(ids)})
}

func (s *Server) handleAdminPayments(w http.ResponseWriter, r *http.Request, _ int64) {
	ps, err := s.Store.ListPayments(r.Context(), 200)
	if err != nil {
		httpErr(w, 500, "xatolik")
		return
	}
	type row struct {
		ChargeID  string    `json:"charge_id"`
		OwnerID   int64     `json:"owner_id"`
		Amount    int       `json:"amount"`
		Currency  string    `json:"currency"`
		Recurring bool      `json:"recurring"`
		Refunded  bool      `json:"refunded"`
		Donation  bool      `json:"donation"` // hayriya xayriyasi (obuna emas)
		CreatedAt time.Time `json:"created_at"`
	}
	out := make([]row, 0, len(ps))
	for _, p := range ps {
		out = append(out, row{p.ChargeID, p.OwnerID, p.Amount, p.Currency, p.Recurring, p.Refunded, p.IsDonation(), p.CreatedAt})
	}
	writeJSON(w, 200, map[string]any{"payments": out})
}

func (s *Server) handleAdminRefund(w http.ResponseWriter, r *http.Request, admin int64) {
	var in struct {
		ChargeID string `json:"charge_id"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.ChargeID == "" || len(in.ChargeID) > 128 {
		httpErr(w, 400, "charge_id noto'g'ri")
		return
	}
	if s.Billing == nil {
		httpErr(w, 409, "to'lov tizimi yoqilmagan")
		return
	}
	p, err := s.Billing.Refund(context.WithoutCancel(r.Context()), in.ChargeID)
	if err != nil {
		httpErr(w, 400, "qaytarib bo'lmadi: "+err.Error())
		return
	}
	s.adminAudit(r.Context(), admin, p.OwnerID, "admin_refund", fmt.Sprintf("%d %s", p.Amount, p.Currency))
	s.notifyUser(r.Context(), p.OwnerID, refundText(p))
	writeJSON(w, 200, map[string]any{"ok": true, "amount": p.Amount, "owner_id": p.OwnerID})
}
