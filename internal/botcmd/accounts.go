package botcmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/bot"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// Bir nechta Telegram akkaunt (Pro).
//
// Qo'shish oqimi (akkaunt egasining roziligi shart — begona akkauntni "ulab olish"dan himoya):
//  1. Asosiy akkaunt Mini App'da bog'lash havolasini oladi: t.me/<bot>?start=link_<token> (30 daqiqa).
//  2. Havolani QO'SHILADIGAN akkauntdan ochadi — bot shu akkauntga rozilik so'rovini ko'rsatadi.
//  3. «Roziman» bosilsa, akkaunt bog'lanadi; keyin Mini App'da shu akkaunt Telegram'ga ulanadi
//     (login faqat aynan shu akkaunt tasdiqlasa o'tadi — self.id tekshiruvi).
//
// Qo'shimcha akkauntning bot xabarlari (qoralama, xulosa, eslatmalar) asosiy akkaunt chatiga
// «👤 <nom>» belgisi bilan keladi; tugmalar u yerdan ishlaydi.

// LinkStartPrefix — /start payload'i.
const LinkStartPrefix = "link_"

// Route — owner qo'shimcha akkaunt bo'lsa, xabar asosiy akkaunt chatiga nom belgisi bilan boradi.
func Route(ctx context.Context, st store.Store, owner int64, text string) (int64, string) {
	if l, err := st.GetLink(ctx, owner); err == nil {
		label := l.Label
		if label == "" {
			label = "Qo'shimcha akkaunt"
		}
		return l.ParentID, "👤 " + label + "\n\n" + text
	}
	return owner, text
}

// accounts — from va unga bog'langan qo'shimcha akkauntlar (from birinchi).
func (h *Handler) accounts(ctx context.Context, from int64) []int64 {
	out := []int64{from}
	if ls, err := h.Store.ListLinks(ctx, from); err == nil {
		for _, l := range ls {
			out = append(out, l.ChildID)
		}
	}
	return out
}

// findDraft — qoralama from'ning o'zida yoki qo'shimcha akkauntlaridan birida.
func (h *Handler) findDraft(ctx context.Context, from, id int64) (int64, store.Draft, error) {
	for _, o := range h.accounts(ctx, from) {
		if d, err := h.Store.GetDraft(ctx, o, id); err == nil {
			return o, d, nil
		}
	}
	return 0, store.Draft{}, store.ErrNotFound
}

func (h *Handler) findAction(ctx context.Context, from, id int64) (int64, store.Action, error) {
	for _, o := range h.accounts(ctx, from) {
		if a, err := h.Store.GetAction(ctx, o, id); err == nil {
			return o, a, nil
		}
	}
	return 0, store.Action{}, store.ErrNotFound
}

// linkCheck — from (qo'shiladigan akkaunt) token bo'yicha bog'lana oladimi. Xato matni foydalanuvchiga.
func (h *Handler) linkCheck(ctx context.Context, from int64, token string) (store.LinkToken, string) {
	lt, err := h.Store.GetLinkToken(ctx, store.TokenHash(token))
	if err != nil || time.Now().After(lt.ExpiresAt) {
		return lt, "Havola eskirgan yoki noto'g'ri. Asosiy akkauntingizdagi Kotib ilovasida yangisini oling."
	}
	if lt.ParentID == from {
		return lt, "Bu havolani QO'SHMOQCHI bo'lgan boshqa Telegram akkauntingizdan oching (Telegram'da akkauntni almashtirib)."
	}
	if l, err := h.Store.GetLink(ctx, from); err == nil {
		if l.ParentID == lt.ParentID {
			return lt, "Bu akkaunt allaqachon ulangan ✅"
		}
		return lt, "Bu akkaunt boshqa Kotib akkauntiga ulangan."
	}
	if ls, _ := h.Store.ListLinks(ctx, from); len(ls) > 0 {
		return lt, "Bu akkauntga boshqa akkauntlar ulangan — uni qo'shimcha qilib bo'lmaydi."
	}
	if acc, err := h.Store.GetAccount(ctx, from); err == nil && acc.ConsentAt != nil {
		return lt, "Bu akkaunt Kotib'da alohida ishlatilmoqda. Qo'shish uchun avval shu akkauntda ilovani ochib, «Uzish va o'chirish»ni bosing."
	}
	ls, _ := h.Store.ListLinks(ctx, lt.ParentID)
	if !h.Billing.CanLink(ctx, lt.ParentID, len(ls)) {
		return lt, fmt.Sprintf("Asosiy akkauntda Pro tarif kerak (ko'pi bilan %d ta qo'shimcha akkaunt).", billing.MaxLinkedAccounts)
	}
	return lt, ""
}

func (h *Handler) linkStart(ctx context.Context, from int64, token string) bot.Reply {
	lt, msg := h.linkCheck(ctx, from, token)
	if msg != "" {
		return bot.Reply{Text: msg}
	}
	label := lt.Label
	if label == "" {
		label = "qo'shimcha akkaunt"
	}
	text := fmt.Sprintf(`🔗 Akkauntni Kotib'ga ulash — «%s»

Shu Telegram akkauntingiz boshqa akkauntingizdagi Kotib'ga qo'shiladi:
• AI kotib shu akkauntga kelgan shaxsiy xabarlarga javob qoralamasini tayyorlaydi (siz yoqsangiz — yuboradi);
• suhbatdoshlaringiz xabarlari javob tayyorlash uchun AI xizmatiga (Google Gemini) yuboriladi;
• boshqaruv va qoralamalar asosiy akkauntingizdagi botda bo'ladi.

Faqat o'zingizning akkauntingiz bo'lsa tasdiqlang. Kimdir sizdan buni so'rayotgan bo'lsa — rad eting.`, label)
	return bot.Reply{Text: text, Buttons: []bot.Button{
		{Text: "✅ Roziman", Data: "lnk:" + token + ":y"},
		{Text: "✖️ Rad etish", Data: "lnk:" + token + ":n"},
	}}
}

func (h *Handler) linkCallback(ctx context.Context, from int64, data string) bot.Answer {
	rest, _ := strings.CutPrefix(data, "lnk:")
	token, ans, _ := strings.Cut(rest, ":")
	if ans != "y" && ans != "n" {
		return bot.Answer{}
	}
	lt, msg := h.linkCheck(ctx, from, token)
	if msg != "" {
		return bot.Answer{Toast: "Bajarib bo'lmadi", Edit: msg}
	}
	hash := store.TokenHash(token)
	if ans == "n" {
		_ = h.Store.DeleteLinkToken(ctx, hash)
		h.notify(ctx, lt.ParentID, "Qo'shimcha akkaunt ulanishi rad etildi.")
		return bot.Answer{Toast: "Rad etildi", Edit: "✖️ Ulash rad etildi."}
	}
	added, err := h.Store.AddLink(ctx, store.Link{ChildID: from, ParentID: lt.ParentID, Label: lt.Label})
	if err != nil || !added {
		return bot.Answer{Toast: "Xatolik", Edit: "Ulab bo'lmadi, qaytadan urinib ko'ring."}
	}
	_ = h.Store.DeleteLinkToken(ctx, hash)
	now := time.Now()
	if err := h.Store.UpsertAccount(ctx, store.Account{OwnerID: from, Status: store.StatusPending, ConsentAt: &now}); err != nil {
		_ = h.Store.DeleteLink(ctx, from)
		return bot.Answer{Toast: "Xatolik", Edit: "Ulab bo'lmadi, qaytadan urinib ko'ring."}
	}
	h.Store.Audit(ctx, from, "consent", "linked")
	h.Store.Audit(ctx, lt.ParentID, "account_linked", lt.Label)
	h.notify(ctx, lt.ParentID, fmt.Sprintf("✅ «%s» akkaunti rozilik berdi. Ilovada Hisob → Akkauntlar bo'limida uni tanlab, Telegram'ga ulashni yakunlang.", lt.Label))
	return bot.Answer{Toast: "Tasdiqlandi", Edit: "✅ Tasdiqlandi. Endi asosiy akkauntingizdagi Kotib ilovasida ulashni yakunlang (Hisob → Akkauntlar)."}
}

func (h *Handler) notify(ctx context.Context, owner int64, text string) {
	if h.Notify != nil {
		h.Notify(ctx, owner, text, true)
	}
}
