// Package botcmd: boshqaruv botining buyruqlari va to'lov hodisalari (bot.Handler).
package botcmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/bot"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// Controller — sessiyani to'xtatish/davom ettirish (userbot.Manager).
type Controller interface {
	Pause(owner int64)
	Resume(ctx context.Context, owner int64) error
}

type Handler struct {
	Store   store.Store
	Billing *billing.Service
	Ctl     Controller
	Notify  billing.Notifier
	Admins  map[int64]bool // ADMIN_IDS: /stats, /refund
	Log     *slog.Logger
	Loc     *time.Location // sanalarni ko'rsatish uchun (default Asia/Tashkent)
}

var _ bot.Handler = (*Handler)(nil)

// Commands — bot menyusi (setMyCommands).
var Commands = []bot.Command{
	{Name: "start", Description: "Kotibni ochish"},
	{Name: "status", Description: "Holat va qoralamalar"},
	{Name: "pause", Description: "Kotibni to'xtatish"},
	{Name: "resume", Description: "Kotibni davom ettirish"},
	{Name: "obuna", Description: "Obuna (Telegram Stars)"},
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

Sozlamalar, qoidalar va qoralamalar — "Ochish" tugmasi orqali ilovada.`

func (h *Handler) Command(ctx context.Context, from int64, cmd, args string) bot.Reply {
	switch cmd {
	case "start":
		text := "Salom! Men kotibingizman: shaxsiy xabarlaringizga javob qoralamasini tayyorlayman, siz tasdiqlaganingizdan keyingina yuboriladi.\n\nBoshlash uchun pastdagi tugmani bosing."
		if h.Billing.Enabled() && h.Billing.TrialDays > 0 {
			text += fmt.Sprintf("\n\nBirinchi %d kun — bepul.", h.Billing.TrialDays)
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
		return fmt.Sprintf("Obuna: %s gacha (yangilanmaydi)\n", h.date(*st.PaidUntil))
	case st.Paid:
		return fmt.Sprintf("Obuna: faol, keyingi to'lov %s\n", h.date(*st.PaidUntil))
	case st.OnTrial:
		return fmt.Sprintf("Bepul sinov: %s gacha (%d kun qoldi)\n", h.date(*st.TrialUntil), st.DaysLeft)
	default:
		return "Obuna: faol emas — /obuna\n"
	}
}

func (h *Handler) pause(ctx context.Context, owner int64) bot.Reply {
	if _, err := h.Store.GetAccount(ctx, owner); err != nil {
		return bot.Reply{Text: "Akkaunt ulanmagan.", OpenApp: true}
	}
	h.Ctl.Pause(owner)
	_ = h.Store.SetStatus(ctx, owner, store.StatusPaused, "mijoz to'xtatdi")
	h.Store.Audit(ctx, owner, "paused", "bot")
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
	link, err := h.Billing.InvoiceLink(ctx, owner)
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

func (h *Handler) stats(ctx context.Context) bot.Reply {
	st, err := h.Store.Stats(ctx, time.Now())
	if err != nil {
		return bot.Reply{Text: "Xatolik: statistika olinmadi."}
	}
	return bot.Reply{Text: fmt.Sprintf("Akkauntlar: %d\nFaol: %d\nTo'lagan (faol): %d\nJami tushum: %d ⭐",
		st.Accounts, st.Active, st.PaidActive, st.StarsTotal)}
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
		h.Notify(ctx, p.OwnerID, fmt.Sprintf("%d ⭐ hisobingizga qaytarildi. Obuna to'xtatildi.", p.Amount), false)
	}
	return bot.Reply{Text: fmt.Sprintf("Qaytarildi: %d ⭐ (foydalanuvchi %d)", p.Amount, p.OwnerID)}
}

func (h *Handler) PreCheckout(ctx context.Context, from int64, currency string, amount int, payload string) error {
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
	text := "To'lov qabul qilindi, rahmat! ⭐\n"
	if p.Recurring && !p.FirstRecurring {
		text = "Obuna avtomatik yangilandi ⭐\n"
	}
	h.Notify(ctx, from, text+h.subscriptionLine(ctx, from), true)
}

func (h *Handler) Refunded(ctx context.Context, from int64, chargeID string) {
	if err := h.Billing.Refunded(ctx, from, chargeID); err != nil {
		h.log().Error("refund record", "owner", from, "err", err)
	}
}
