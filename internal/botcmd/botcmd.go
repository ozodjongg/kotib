// Package botcmd: boshqaruv botining buyruqlari va to'lov hodisalari (bot.Handler).
package botcmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/bot"
	"github.com/ozodjongg/secretar_ai_agent/internal/google"
	"github.com/ozodjongg/secretar_ai_agent/internal/ics"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// Controller — sessiyani to'xtatish/davom ettirish (userbot.Manager).
type Controller interface {
	Pause(owner int64)
	Resume(ctx context.Context, owner int64) error
	// SendDraftByID qoralamani yuboradi (text bo'sh bo'lmasa — shu matn); hal qilingan bo'lsa store.ErrNotFound.
	SendDraftByID(ctx context.Context, owner, id int64, text string) error
	RejectDraft(ctx context.Context, owner, id int64) error
}

type Handler struct {
	Store   store.Store
	Billing *billing.Service
	Ctl     Controller
	Notify  billing.Notifier
	Admins  map[int64]bool // ADMIN_IDS: /stats, /refund
	Log     *slog.Logger
	Loc     *time.Location // sanalarni ko'rsatish uchun (default Asia/Tashkent)
	// Google — tasdiqlangan amallarni bajarish (google.Client); nil → o'chiq.
	Google Executor
	// ICS — Google ulanmagan mijozlar uchun .ics havolalari; nil → o'chiq.
	ICS *ics.Signer
	// BotUsername — taklif havolasi uchun (t.me/<bot>?start=ref_...); bo'sh → /taklif havolasiz.
	BotUsername string
	// AI — /sor va soya rejimi tugmalari (userbot.Manager); nil → o'chiq.
	AI Assistant
}

// Assistant — egasi uchun AI yordamchi (userbot.Manager).
type Assistant interface {
	// Ask savoldagi kontakt bilan yozishmadan javob beradi (Pro).
	Ask(ctx context.Context, owner int64, question string) (string, error)
	// ApplyShadowChoice soya rejimi natijasidagi tanlovni qo'llaydi (userbot.ShadowAuto / ShadowDraft).
	ApplyShadowChoice(ctx context.Context, owner int64, choice string) (string, error)
}

// Executor — egasi tasdiqlagan amalni Google'da bajaradi.
type Executor interface {
	Connected(ctx context.Context, owner int64) bool
	Execute(ctx context.Context, a store.Action) (string, error)
}

var _ bot.Handler = (*Handler)(nil)

// Commands — bot menyusi (setMyCommands).
var Commands = []bot.Command{
	{Name: "start", Description: "Kotibni ochish"},
	{Name: "status", Description: "Holat va qoralamalar"},
	{Name: "pause", Description: "Kotibni to'xtatish"},
	{Name: "resume", Description: "Kotibni davom ettirish"},
	{Name: "obuna", Description: "Obuna (Telegram Stars)"},
	{Name: "taklif", Description: "Do'stni taklif qilish — bonus kunlar"},
	{Name: "hayriya", Description: "Hayriya: hisobot va xayriya qilish"},
	{Name: "sor", Description: "Yozishma haqida so'rash: /sor Aziz bilan nima kelishdik?"},
	{Name: "help", Description: "Yordam"},
}

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) date(t time.Time) string {
	loc := h.Loc
	if loc == nil {
		loc, _ = time.LoadLocation("Asia/Tashkent")
	}
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format("02.01.2006 15:04")
}

const helpText = `Kotib — Telegram'dagi shaxsiy xabarlaringizga AI yordamida javob qoralamasini tayyorlaydi.

/start — ilovani ochish
/status — holat, qoralamalar va obuna
/pause — kotibni darhol to'xtatish
/resume — davom ettirish
/obuna — obunani yoqish (Telegram Stars)
/taklif — do'stni taklif qilish (bonus kunlar)
/hayriya — hayriya hisoboti va xayriya qilish
/sor <savol> — yozishma haqida so'rash, masalan: /sor Aziz bilan nima kelishdik? (Pro)
  /sor yozish shart emas: botga oddiy xabar yozsangiz ham savol deb qabul qilinadi.

Sozlamalar, qoidalar va qoralamalar — "Ochish" tugmasi orqali ilovada.`

// BannedText — bloklangan foydalanuvchiga bot javobi.
const BannedText = "⛔ Akkauntingiz administrator tomonidan bloklangan, kotib ishlamaydi. Xato deb hisoblasangiz, administrator bilan bog'laning."

func (h *Handler) banned(ctx context.Context, from int64) bool {
	return !h.Admins[from] && h.Billing.Banned(ctx, from)
}

func (h *Handler) Command(ctx context.Context, from int64, cmd, args string) bot.Reply {
	if h.banned(ctx, from) {
		return bot.Reply{Text: BannedText}
	}
	switch cmd {
	case "start":
		// Deep link: t.me/<bot>?start=pause — Siri / Google Assistant / yorliq orqali.
		switch args {
		case "pause":
			return h.pause(ctx, from)
		case "resume":
			return h.resume(ctx, from)
		}
		if token, ok := strings.CutPrefix(args, LinkStartPrefix); ok {
			return h.linkStart(ctx, from, token)
		}
		trial := h.Billing.TrialDays
		referred := false
		if code, ok := strings.CutPrefix(args, billing.ReferralStartPrefix); ok && h.Billing.AttachReferral(ctx, from, code) {
			referred = true
			trial += billing.ReferralTrialBonus
		}
		text := "Salom! Men kotibingizman: shaxsiy xabarlaringizga javob qoralamasini tayyorlayman, siz tasdiqlaganingizdan keyingina yuboriladi.\n\nBoshlash uchun pastdagi tugmani bosing."
		if h.Billing.Enabled() && h.Billing.TrialDays > 0 {
			if referred {
				text += fmt.Sprintf("\n\n🎁 Do'stingiz taklifi bilan: birinchi %d kun — bepul (odatda %d kun).", trial, h.Billing.TrialDays)
			} else {
				text += fmt.Sprintf("\n\nBirinchi %d kun — bepul.", trial)
			}
		}
		return bot.Reply{Text: text, OpenApp: true}
	case "help":
		return bot.Reply{Text: helpText, OpenApp: true}
	case "status":
		return h.status(ctx, from)
	case "pause":
		return h.pause(ctx, from)
	case "resume":
		return h.resume(ctx, from)
	case "obuna", "subscribe":
		return h.subscribe(ctx, from)
	case "taklif", "invite", "referral":
		return h.invite(ctx, from)
	case "hayriya", "charity", "donate":
		return h.charity(ctx, from, args)
	case "sor", "so'ra", "ask":
		return h.ask(ctx, from, args)
	case "stats":
		if h.Admins[from] {
			return h.stats(ctx)
		}
	case "refund":
		if h.Admins[from] {
			return h.refund(ctx, args)
		}
	}
	return bot.Reply{Text: "Noma'lum buyruq. /help"}
}

var statusNames = map[store.AccountStatus]string{
	store.StatusPending: "ulanmagan",
	store.StatusActive:  "faol ✅",
	store.StatusPaused:  "to'xtatilgan ⏸",
	store.StatusBroken:  "xavfsizlik uchun to'xtatilgan ⚠️",
	store.StatusRevoked: "uzilgan",
}

func (h *Handler) status(ctx context.Context, owner int64) bot.Reply {
	acc, err := h.Store.GetAccount(ctx, owner)
	if errors.Is(err, store.ErrNotFound) {
		return bot.Reply{Text: "Siz hali ro'yxatdan o'tmagansiz. Boshlash uchun ilovani oching.", OpenApp: true}
	} else if err != nil {
		return bot.Reply{Text: "Xatolik, keyinroq urinib ko'ring."}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Holat: %s\n", statusNames[acc.Status])
	if acc.StatusNote != "" {
		fmt.Fprintf(&b, "Izoh: %s\n", acc.StatusNote)
	}
	if ds, err := h.Store.ListDrafts(ctx, owner, store.DraftPending, 100); err == nil {
		fmt.Fprintf(&b, "Kutilayotgan qoralamalar: %d\n", len(ds))
	}
	loc := time.UTC
	if st, err := h.Store.GetSettings(ctx, owner); err == nil {
		loc = st.Location()
	}
	if n, saved, err := store.ReplyStats(ctx, h.Store, owner, store.MonthStart(time.Now(), loc)); err == nil && n > 0 {
		fmt.Fprintf(&b, "Bu oy: %d ta javob, ~%s tejaldi\n", n, store.FormatSaved(saved))
	}
	if ls, err := h.Store.ListLinks(ctx, owner); err == nil && len(ls) > 0 {
		b.WriteString("Qo'shimcha akkauntlar:\n")
		for _, l := range ls {
			st := "ulanmagan"
			if acc, err := h.Store.GetAccount(ctx, l.ChildID); err == nil {
				st = statusNames[acc.Status]
			}
			fmt.Fprintf(&b, "  👤 %s — %s\n", l.Label, st)
		}
	}
	if h.Billing.Enabled() {
		b.WriteString(h.subscriptionLine(ctx, owner))
	}
	return bot.Reply{Text: b.String(), OpenApp: true}
}

func (h *Handler) subscriptionLine(ctx context.Context, owner int64) string {
	st, err := h.Billing.Status(ctx, owner)
	switch {
	case err != nil:
		return ""
	case st.Paid && st.Canceled:
		return fmt.Sprintf("Obuna%s: %s gacha (yangilanmaydi)\n", h.tierLabel(st), h.date(*st.PaidUntil))
	case st.Paid:
		return fmt.Sprintf("Obuna%s: faol, keyingi to'lov %s\n", h.tierLabel(st), h.date(*st.PaidUntil))
	case st.Bonus:
		return fmt.Sprintf("🎁 Bonus kunlar: %s gacha (%d kun qoldi)\n", h.date(*st.BonusUntil), st.DaysLeft)
	case st.OnTrial:
		return fmt.Sprintf("Bepul sinov: %s gacha (%d kun qoldi)\n", h.date(*st.TrialUntil), st.DaysLeft)
	default:
		return "Obuna: faol emas — /obuna\n"
	}
}

// tierLabel — " (Pro)" kabi; bitta tarif rejimida bo'sh.
func (h *Handler) tierLabel(st billing.Status) string {
	if !h.Billing.Tiered() || tierNames[st.Tier] == "" {
		return ""
	}
	return " (" + tierNames[st.Tier] + ")"
}

func (h *Handler) pause(ctx context.Context, owner int64) bot.Reply {
	if _, err := h.Store.GetAccount(ctx, owner); err != nil {
		return bot.Reply{Text: "Akkaunt ulanmagan.", OpenApp: true}
	}
	// Barcha akkauntlar (asosiy va qo'shimcha) birdan to'xtaydi.
	for _, o := range h.accounts(ctx, owner) {
		acc, err := h.Store.GetAccount(ctx, o)
		if err != nil || (o != owner && acc.Status != store.StatusActive) {
			continue
		}
		h.Ctl.Pause(o)
		_ = h.Store.SetStatus(ctx, o, store.StatusPaused, "mijoz to'xtatdi")
		h.Store.Audit(ctx, o, "paused", "bot")
	}
	return bot.Reply{Text: "Kotib to'xtatildi ⏸. Davom ettirish: /resume"}
}

func (h *Handler) resume(ctx context.Context, owner int64) bot.Reply {
	acc, err := h.Store.GetAccount(ctx, owner)
	if err != nil || acc.ConsentAt == nil || acc.Status == store.StatusPending || acc.Status == store.StatusRevoked {
		return bot.Reply{Text: "Avval ilovada Telegram akkauntingizni ulang.", OpenApp: true}
	}
	if err := h.Ctl.Resume(context.WithoutCancel(ctx), owner); err != nil {
		return bot.Reply{Text: "Davom ettirib bo'lmadi: avval akkauntni qayta ulang.", OpenApp: true}
	}
	_ = h.Store.SetStatus(ctx, owner, store.StatusActive, "")
	h.Store.Audit(ctx, owner, "resumed", "bot")
	// Qo'shimcha akkauntlar: faqat to'xtatilganlari (uzilgan/xavfsizlik uchun to'xtaganiga tegilmaydi).
	for _, o := range h.accounts(ctx, owner)[1:] {
		if acc, err := h.Store.GetAccount(ctx, o); err == nil && acc.Status == store.StatusPaused {
			if h.Ctl.Resume(context.WithoutCancel(ctx), o) == nil {
				_ = h.Store.SetStatus(ctx, o, store.StatusActive, "")
				h.Store.Audit(ctx, o, "resumed", "bot")
			}
		}
	}
	text := "Kotib yana ishlayapti ✅"
	if !h.Billing.Entitled(ctx, owner) {
		text += "\n\nDiqqat: obuna faol emas — qoralamalar tayyorlanmaydi. /obuna"
	}
	return bot.Reply{Text: text}
}

func (h *Handler) subscribe(ctx context.Context, owner int64) bot.Reply {
	if !h.Billing.Enabled() {
		return bot.Reply{Text: "Hozircha kotib bepul — obuna shart emas."}
	}
	if _, err := h.Store.GetAccount(ctx, owner); err != nil {
		return bot.Reply{Text: "Avval ilovada ro'yxatdan o'ting.", OpenApp: true}
	}
	if !h.Billing.Tiered() {
		link, err := h.Billing.InvoiceLink(ctx, owner, billing.TierAdvanced)
		if errors.Is(err, billing.ErrAlreadySubscribed) {
			return bot.Reply{Text: "Obunangiz allaqachon faol.\n" + h.subscriptionLine(ctx, owner), OpenApp: true}
		}
		if err != nil {
			h.log().Warn("invoice link", "owner", owner, "err", err)
			return bot.Reply{Text: "To'lov havolasini yaratib bo'lmadi, keyinroq urinib ko'ring."}
		}
		return bot.Reply{
			Text:      fmt.Sprintf("Oylik obuna: %d ⭐ / 30 kun. Har oy avtomatik yangilanadi, istalgan payt bekor qilish mumkin.", h.Billing.Price),
			ButtonURL: link, Button: fmt.Sprintf("⭐ %d — to'lash", h.Billing.Price),
		}
	}
	// Ikki tarif: hozirgi faol tarifdan boshqasi uchun to'lov havolasi (tarifni almashtirish ham shu).
	var links []bot.Link
	for _, t := range []billing.Tier{billing.TierAdvanced, billing.TierPro} {
		link, err := h.Billing.InvoiceLink(ctx, owner, t)
		if errors.Is(err, billing.ErrAlreadySubscribed) {
			continue
		}
		if err != nil {
			h.log().Warn("invoice link", "owner", owner, "tier", t, "err", err)
			return bot.Reply{Text: "To'lov havolasini yaratib bo'lmadi, keyinroq urinib ko'ring."}
		}
		links = append(links, bot.Link{Text: fmt.Sprintf("⭐ %s — %d", tierNames[t], h.Billing.PriceOf(t)), URL: link})
	}
	text := fmt.Sprintf(tiersText, h.Billing.PriceOf(billing.TierAdvanced), h.Billing.PriceOf(billing.TierPro))
	if line := h.subscriptionLine(ctx, owner); h.Billing.Tier(ctx, owner) == billing.TierAdvanced || h.Billing.Tier(ctx, owner) == billing.TierPro {
		text += "\n\n" + line + "Boshqa tarifga o'tsangiz, qolgan kunlaringiz narx nisbatida yangi tarifga o'tkaziladi."
	}
	return bot.Reply{Text: text, Links: links, OpenApp: true}
}

var tierNames = map[billing.Tier]string{billing.TierAdvanced: "Advanced", billing.TierPro: "Pro", billing.TierTrial: "Sinov"}

const tiersText = `Tariflar (30 kun, har oy avtomatik yangilanadi, istalgan payt bekor qilish mumkin):

⭐ Advanced — %d ⭐
• cheksiz javob qoralamalari
• uzun suhbat konteksti (20 xabar)
• avto rejim, qoidalar, holatlar
• kechki xulosa, va'dalar, taqvimga .ics

🚀 Pro — %d ⭐
• Advanced'dagi hammasi
• 🎤 ovozli xabarlarga javob
• 📅 Google Calendar, Contacts, Tasks
• 📱 telefon avtomatikasi (Shortcuts / MacroDroid)
• eng uzun kontekst (40 xabar)`

func (h *Handler) stats(ctx context.Context) bot.Reply {
	st, err := h.Store.Stats(ctx, time.Now())
	if err != nil {
		return bot.Reply{Text: "Xatolik: statistika olinmadi."}
	}
	return bot.Reply{Text: fmt.Sprintf("Akkauntlar: %d\nFaol: %d\nTo'lagan (faol): %d\nJami tushum: %d ⭐\nTaklif orqali kelgan: %d (to'lagan: %d)",
		st.Accounts, st.Active, st.PaidActive, st.StarsTotal, st.Referrals, st.RefPaid)}
}

func (h *Handler) invite(ctx context.Context, owner int64) bot.Reply {
	if !h.Billing.ReferralsEnabled() {
		return bot.Reply{Text: "Hozircha kotib bepul — taklif bonuslari yo'q. Lekin do'stlaringizga bemalol ulashing!"}
	}
	if _, err := h.Store.GetAccount(ctx, owner); err != nil {
		return bot.Reply{Text: "Avval ilovada ro'yxatdan o'ting.", OpenApp: true}
	}
	info, err := h.Billing.Referral(ctx, owner)
	link := billing.ReferralLink(h.BotUsername, info.Code)
	if err != nil || link == "" {
		if err != nil {
			h.log().Warn("referral", "owner", owner, "err", err)
		}
		return bot.Reply{Text: "Taklif havolasini yaratib bo'lmadi, keyinroq urinib ko'ring."}
	}
	text := fmt.Sprintf(`🎁 Do'stlaringizni taklif qiling

• Do'stingiz %d kun bepul sinaydi (odatdagidan %d kun ko'p)
• U Telegram akkauntini ulasa — sizga +%d kun
• U obuna bo'lsa — sizga +%d kun (bir oy bepul)

Sizning havolangiz:
%s

Taklif qilganlar: %d · ulanganlar: %d · obuna bo'lganlar: %d
Jami olgan bonusingiz: %d kun`,
		info.TrialDays, info.TrialBonus, info.ActivatedDays, info.PaidDays, link,
		info.Invited, info.Activated, info.Paid, info.EarnedDays)
	return bot.Reply{Text: text, OpenApp: true}
}

func (h *Handler) refund(ctx context.Context, chargeID string) bot.Reply {
	if chargeID == "" || len(chargeID) > 128 {
		return bot.Reply{Text: "Foydalanish: /refund <telegram_payment_charge_id>"}
	}
	p, err := h.Billing.Refund(ctx, chargeID)
	if err != nil {
		return bot.Reply{Text: "Qaytarib bo'lmadi: " + err.Error()}
	}
	if h.Notify != nil {
		h.Notify(ctx, p.OwnerID, RefundText(p.Amount, p.IsDonation()), false)
	}
	return bot.Reply{Text: fmt.Sprintf("Qaytarildi: %d ⭐ (foydalanuvchi %d)", p.Amount, p.OwnerID)}
}

func (h *Handler) PreCheckout(ctx context.Context, from int64, currency string, amount int, payload string) error {
	if h.banned(ctx, from) {
		return errors.New("akkaunt bloklangan")
	}
	return h.Billing.PreCheckout(ctx, from, currency, amount, payload)
}

func (h *Handler) Paid(ctx context.Context, from int64, p billing.PaymentInfo) {
	if err := h.Billing.PaymentReceived(ctx, from, p); err != nil {
		// Pul yechilgan, lekin yozilmadi — admin qo'lda tekshirishi uchun charge_id logda.
		h.log().Error("payment not recorded", "owner", from, "charge", p.ChargeID, "err", err)
		if h.Notify != nil {
			h.Notify(ctx, from, "To'lov qabul qilindi, lekin qayd etishda xatolik bo'ldi. Tez orada tuzatamiz — xavotir olmang.", false)
		}
		return
	}
	if h.Notify == nil {
		return
	}
	if billing.IsDonation(p.Payload) {
		h.Notify(ctx, from, fmt.Sprintf("💚 Rahmat! %d ⭐ hayriyaga qo'shildi. Hisobot: /hayriya", p.Amount), false)
		return
	}
	text := "To'lov qabul qilindi, rahmat! ⭐\n"
	if p.Recurring && !p.FirstRecurring {
		text = "Obuna avtomatik yangilandi ⭐\n"
	}
	text += h.subscriptionLine(ctx, from)
	if share := h.Billing.CharityShare(p.Amount); share > 0 {
		text += fmt.Sprintf("💚 To'lovingizdan %d ⭐ hayriyaga ajratildi.\n", share)
	}
	h.Notify(ctx, from, text, true)
}

// RefundText — to'lov qaytarilgani haqida mijozga xabar.
func RefundText(amount int, donation bool) string {
	if donation {
		return fmt.Sprintf("%d ⭐ hisobingizga qaytarildi (hayriya xayriyasi).", amount)
	}
	return fmt.Sprintf("%d ⭐ hisobingizga qaytarildi. Obuna to'xtatildi.", amount)
}

func (h *Handler) Refunded(ctx context.Context, from int64, chargeID string) {
	if err := h.Billing.Refunded(ctx, from, chargeID); err != nil {
		h.log().Error("refund record", "owner", from, "err", err)
	}
}

// ActionButtons — taklif qilingan amal uchun tasdiq tugmalari.
func ActionButtons(id int64) []bot.Button {
	sid := strconv.FormatInt(id, 10)
	return []bot.Button{{Text: "✅ Qo'shish", Data: "act:" + sid + ":y"}, {Text: "✖️ Kerak emas", Data: "act:" + sid + ":n"}}
}

// Callback — amalni tasdiqlash/rad etish tugmalari. Amal faqat egasiga tegishli bo'lsa
// topiladi; holat atomar o'zgaradi — ikki marta bosish ikki tadbir yaratmaydi.
func (h *Handler) Callback(ctx context.Context, from int64, data string) bot.Answer {
	if h.banned(ctx, from) {
		return bot.Answer{Toast: "Akkaunt bloklangan"}
	}
	if strings.HasPrefix(data, "lnk:") {
		return h.linkCallback(ctx, from, data)
	}
	if strings.HasPrefix(data, "shd:") {
		toast, edit := h.shadowCallback(ctx, from, data)
		return bot.Answer{Toast: toast, Edit: edit}
	}
	if strings.HasPrefix(data, "d:") {
		toast, edit := h.draftCallback(ctx, from, data)
		return bot.Answer{Toast: toast, Edit: edit}
	}
	rest, ok := strings.CutPrefix(data, "act:")
	if !ok {
		return bot.Answer{}
	}
	sid, ans, _ := strings.Cut(rest, ":")
	id, err := strconv.ParseInt(sid, 10, 64)
	if err != nil || (ans != "y" && ans != "n") {
		return bot.Answer{}
	}
	owner, a, err := h.findAction(ctx, from, id)
	if err != nil {
		return bot.Answer{Toast: "Topilmadi yoki eskirgan"}
	}
	from = owner // qo'shimcha akkauntning amali — o'sha akkaunt nomidan bajariladi
	summary, _, _ := strings.Cut(google.Describe(a), "\n\n")
	if ans == "n" {
		if err := h.Store.SetActionStatus(ctx, from, id, store.ActionPending, store.ActionRejected); err != nil {
			return bot.Answer{Toast: "Allaqachon hal qilingan"}
		}
		h.Store.Audit(ctx, from, "action_rejected", string(a.Kind))
		return bot.Answer{Toast: "Bekor qilindi", Edit: summary + "\n\n✖️ Qo'shilmadi"}
	}
	useGoogle := h.Google != nil && h.Google.Connected(ctx, from)
	if !useGoogle && (a.Kind != store.ActionEvent || h.ICS == nil) {
		return bot.Answer{Toast: "Google ulanmagan"}
	}
	if err := h.Store.SetActionStatus(ctx, from, id, store.ActionPending, store.ActionDone); err != nil {
		return bot.Answer{Toast: "Allaqachon hal qilingan"}
	}
	if !useGoogle {
		// Google'siz: imzolangan .ics havola — telefon taqvimi o'zi "qo'shish" oynasini ochadi.
		h.Store.Audit(ctx, from, "action_done", "ics")
		return bot.Answer{Toast: "Tayyor", Edit: summary + "\n\nTelefoningiz taqvimiga qo'shish uchun pastdagi tugmani bosing.",
			URL: h.ICS.URL(from, id), URLText: "📅 Taqvimga qo'shish"}
	}
	res, err := h.Google.Execute(context.WithoutCancel(ctx), a)
	if err != nil {
		_ = h.Store.SetActionStatus(ctx, from, id, store.ActionDone, store.ActionFailed)
		h.Store.Audit(ctx, from, "action_failed", string(a.Kind))
		h.log().Warn("google execute", "owner", from, "err", err)
		return bot.Answer{Toast: "Xatolik", Edit: summary + "\n\n⚠️ " + google.UserMessage(err)}
	}
	h.Store.Audit(ctx, from, "action_done", string(a.Kind))
	return bot.Answer{Toast: res, Edit: summary + "\n\n" + res}
}

// ---- Qoralama bot chatida ----

const maxCardPart = 1200 // Telegram xabari 4096 belgigacha: kontakt matni va javob qisqartiriladi

// DraftCard — qoralama kartasining matni va tugmalari.
func DraftCard(d store.Draft, contactName string) (string, []bot.Button) {
	name := strings.TrimSpace(contactName)
	if name == "" {
		name = "Kontakt"
	}
	text := "✍️ Qoralama · " + name + "\n\n💬 " + clip(d.Incoming, maxCardPart) + "\n\n➡️ " + clip(d.Reply, maxCardPart)
	if d.Reason != "" && d.Reason != "draft rejimi" {
		text += "\n\n(" + d.Reason + ")"
	}
	text += "\n\nO'zgartirish uchun shu xabarga javob qilib yangi matnni yozing."
	sid := strconv.FormatInt(d.ID, 10)
	return text, []bot.Button{{Text: "📤 Yuborish", Data: "d:" + sid + ":s"}, {Text: "✖️ Rad etish", Data: "d:" + sid + ":r"}}
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

func parseDraftData(data string) (int64, string, bool) {
	rest, ok := strings.CutPrefix(data, "d:")
	if !ok {
		return 0, "", false
	}
	sid, op, _ := strings.Cut(rest, ":")
	id, err := strconv.ParseInt(sid, 10, 64)
	if err != nil || id <= 0 || (op != "s" && op != "r") {
		return 0, "", false
	}
	return id, op, true
}

func (h *Handler) draftCallback(ctx context.Context, from int64, data string) (string, string) {
	id, op, ok := parseDraftData(data)
	if !ok {
		return "", ""
	}
	owner, d, err := h.findDraft(ctx, from, id)
	if err != nil {
		return "Topilmadi", ""
	}
	from = owner
	if op == "r" {
		if err := h.Ctl.RejectDraft(ctx, from, id); err != nil {
			return "Allaqachon hal qilingan", ""
		}
		return "Rad etildi", "✖️ Rad etildi\n\n" + clip(d.Reply, maxCardPart)
	}
	return h.sendDraft(ctx, from, d, "")
}

func (h *Handler) sendDraft(ctx context.Context, from int64, d store.Draft, text string) (string, string) {
	err := h.Ctl.SendDraftByID(context.WithoutCancel(ctx), from, d.ID, text)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "Allaqachon hal qilingan", ""
	case err != nil:
		h.log().Warn("bot draft send", "owner", from, "err", err)
		return "Yuborib bo'lmadi, keyinroq urinib ko'ring", ""
	}
	sent := d.Reply
	if strings.TrimSpace(text) != "" {
		sent = text
	}
	return "Yuborildi", "📤 Yuborildi\n\n" + clip(sent, maxCardPart)
}

// okWords — "asl qoralamani yubor" ma'nosidagi qisqa javoblar.
var okWords = map[string]bool{"ok": true, "ок": true, "ha": true, "+": true, "✅": true, "👍": true, "yubor": true}

// ReplyTo — qoralama kartasiga javob: "ok" — asl matn, boshqa matn — o'sha matn yuboriladi.
func (h *Handler) ReplyTo(ctx context.Context, from int64, data, text string) (bot.Reply, string) {
	if h.banned(ctx, from) {
		return bot.Reply{}, ""
	}
	id, _, ok := parseDraftData(data)
	if !ok {
		return bot.Reply{}, ""
	}
	owner, d, err := h.findDraft(ctx, from, id)
	if err != nil || d.Status != store.DraftPending {
		return bot.Reply{Text: "Bu qoralama allaqachon hal qilingan."}, ""
	}
	from = owner
	t := strings.TrimSpace(text)
	if okWords[strings.ToLower(t)] {
		t = ""
	} else if len([]rune(t)) > 4000 {
		return bot.Reply{Text: "Matn juda uzun (4000 belgigacha)."}, ""
	}
	toast, edit := h.sendDraft(ctx, from, d, t)
	return bot.Reply{Text: toast}, edit
}
