// Package billing: Telegram Stars obunasi — bepul sinov, oylik obuna, to'lovni
// tasdiqlash, bekor qilish va qaytarish (refund).
//
// Xavfsizlik tamoyillari:
//   - Obuna faqat Telegram yuborgan successful_payment asosida faollashadi; Mini App
//     yoki mijoz "to'ladim" deya olmaydi.
//   - pre_checkout_query'da payload egasi, valyuta (XTR) va summa tekshiriladi.
//   - Har bir telegram_payment_charge_id bir marta hisobga olinadi (takroriy update'dan himoya).
//   - Payload tasodifiy nonce bilan — taxmin qilib bo'lmaydi.
package billing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

const (
	Currency      = "XTR"   // Telegram Stars
	PeriodSeconds = 2592000 // Telegram hozircha faqat 30 kunlik obunani qo'llaydi
	Period        = 30 * 24 * time.Hour
	MaxPrice      = 10000 // Telegram cheklovi
	remindBefore  = 24 * time.Hour
	payloadPrefix = "sub:"
)

var (
	ErrDisabled          = errors.New("to'lov tizimi yoqilmagan")
	ErrAlreadySubscribed = errors.New("obuna allaqachon faol")
	ErrNoSubscription    = errors.New("faol obuna yo'q")
)

// InvoiceLink — createInvoiceLink parametrlari (Stars obunasi).
type InvoiceLink struct {
	Title, Description, Payload, Label string
	Amount                             int
	SubscriptionPeriod                 int
}

// API — Telegram Bot API'ning to'lov qismi (bot.Bot implementatsiya qiladi).
type API interface {
	CreateInvoiceLink(ctx context.Context, in InvoiceLink) (string, error)
	EditUserStarSubscription(ctx context.Context, userID int64, chargeID string, canceled bool) error
	RefundStarPayment(ctx context.Context, userID int64, chargeID string) error
}

// Notifier — mijozga bot orqali xabar (userbot.Notifier bilan bir xil imzo).
type Notifier func(ctx context.Context, owner int64, text string, openApp bool)

type Service struct {
	Store store.Store
	API   API
	Price int // Advanced tarif, oyiga Stars; 0 → to'lov o'chiq, xizmat hammaga bepul
	// ProPrice — Pro tarif, oyiga Stars; 0 → bitta tarif (obunada hamma funksiya ochiq).
	ProPrice  int
	TrialDays int
	Exempt    map[int64]bool // to'lovdan ozod (ADMIN_IDS) — o'z botingizni bepul ishlatish/sinash uchun
	// TrialDailyLimit — sinovdagi mijozga kuniga ko'pi bilan shuncha javob tayyorlanadi
	// (bepul sinov xarajatini cheklash). 0 → cheksiz.
	TrialDailyLimit int
	// Notify — referal mukofoti haqida taklif qiluvchiga xabar; nil → xabarsiz.
	Notify Notifier
	Log    *slog.Logger
	Now    func() time.Time
}

// quotaZone — kunlik limit Toshkent vaqti bilan yarim tunda yangilanadi (UTC+5, yozgi vaqt yo'q).
var quotaZone = time.FixedZone("Asia/Tashkent", 5*60*60)

func (s *Service) Enabled() bool { return s != nil && s.Price > 0 }

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Status — Mini App'ga ko'rsatiladigan obuna holati.
type Status struct {
	Enabled    bool       `json:"enabled"`
	Price      int        `json:"price"`     // Advanced
	ProPrice   int        `json:"pro_price"` // 0 — bitta tarif
	Tier       Tier       `json:"tier"`      // hozirgi tarif: trial/advanced/pro yoki bo'sh
	HasPro     bool       `json:"has_pro"`   // Pro funksiyalari ochiq
	TrialDays  int        `json:"trial_days"`
	Active     bool       `json:"active"`      // hozir xizmatdan foydalanish mumkinmi
	Exempt     bool       `json:"exempt"`      // admin: to'lovdan ozod
	OnTrial    bool       `json:"on_trial"`    // faqat sinov muddati hisobiga
	Paid       bool       `json:"paid"`        // to'langan davr ichida
	Bonus      bool       `json:"bonus"`       // referal bonusi hisobiga (to'lovsiz) to'liq xizmat
	BonusUntil *time.Time `json:"bonus_until"` // nil — bonus yo'q
	Canceled   bool       `json:"canceled"`    // avtomatik uzaytirish o'chirilgan
	Renewable  bool       `json:"renewable"`   // bekor qilingan obunani qayta yoqish mumkin
	TrialUntil *time.Time `json:"trial_until"` // nil — sinov boshlanmagan
	PaidUntil  *time.Time `json:"paid_until"`
	Until      *time.Time `json:"until"`
	DaysLeft   int        `json:"days_left"`
	TrialDaily int        `json:"trial_daily_limit"` // sinovda kuniga javoblar limiti; 0 — cheksiz
	Banned     bool       `json:"banned"`            // admin bloklagan: xizmat ko'rsatilmaydi
}

func ptr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (s *Service) Status(ctx context.Context, owner int64) (Status, error) {
	st, err := s.status(ctx, owner)
	if s.Banned(ctx, owner) {
		st.Active, st.HasPro, st.Banned = false, false, true
	}
	return st, err
}

func (s *Service) status(ctx context.Context, owner int64) (Status, error) {
	if !s.Enabled() {
		return Status{Active: true}, nil
	}
	if p, linked := s.payer(ctx, owner); linked {
		st, err := s.Status(ctx, p)
		st.Active = st.Active && st.HasPro // qo'shimcha akkaunt — Pro funksiyasi
		return st, err
	}
	st := Status{Enabled: true, Price: s.Price, ProPrice: s.PriceOf(TierPro), TrialDays: s.TrialDays, TrialDaily: s.TrialDailyLimit}
	if s.Exempt[owner] {
		st.Active, st.Exempt, st.Tier, st.HasPro = true, true, TierPro, true
		return st, nil
	}
	sub, err := s.subscription(ctx, owner)
	if errors.Is(err, store.ErrNotFound) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	now := s.now()
	st.TrialUntil, st.PaidUntil, st.Until = ptr(sub.TrialUntil), ptr(sub.PaidUntil), ptr(sub.Until())
	if sub.BonusUntil.After(now) {
		st.BonusUntil = ptr(sub.BonusUntil)
	}
	st.Paid = sub.PaidUntil.After(now)
	st.Active = sub.Until().After(now)
	st.Bonus = !st.Paid && sub.BonusUntil.After(now)
	st.OnTrial = st.Active && !st.Paid && !st.Bonus
	st.Canceled = sub.Canceled
	st.Renewable = st.Paid && sub.Canceled && sub.ChargeID != ""
	st.Tier = s.tierAt(sub, now)
	st.HasPro = st.Tier == TierPro || st.Tier == TierTrial || (st.Tier == TierAdvanced && !s.Tiered())
	if st.Active {
		st.DaysLeft = int((sub.Until().Sub(now) + 24*time.Hour - time.Second) / (24 * time.Hour))
	}
	return st, nil
}

// Entitled — AI javob tayyorlashi mumkinmi. Baza xatosida yopiq (false): to'lanmagan
// xizmat ko'rsatilmasin; xato logga yoziladi.
func (s *Service) Entitled(ctx context.Context, owner int64) bool {
	if s.Banned(ctx, owner) {
		return false
	}
	if !s.Enabled() || s.Exempt[owner] {
		return true
	}
	if p, linked := s.payer(ctx, owner); linked {
		return s.HasPro(ctx, p) && s.Entitled(ctx, p)
	}
	sub, err := s.subscription(ctx, owner)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log().Error("billing entitled", "owner", owner, "err", err)
		}
		return false
	}
	return sub.Until().After(s.now())
}

// Kontekst: model har javobda chatdagi oxirgi shuncha xabarni ko'radi. Obunada ko'proq —
// uzun suhbatda oldingi savollar unutilmaydi (pullik ustunlik); sinovda xarajat kamroq.
const (
	HistoryPaid  = 20
	HistoryTrial = 8
)

// HistoryLimit egasi uchun kontekst uzunligi: Pro tarif — HistoryPro; to'langan obuna (yoki bonus),
// admin yoki to'lov o'chiq — HistoryPaid; sinov (yoki holat noma'lum) — HistoryTrial.
func (s *Service) HistoryLimit(ctx context.Context, owner int64) int {
	if !s.Enabled() || s.Exempt[owner] {
		return HistoryPaid
	}
	if p, linked := s.payer(ctx, owner); linked {
		return s.HistoryLimit(ctx, p)
	}
	sub, err := s.Store.GetSubscription(ctx, owner)
	if err == nil && sub.Premium(s.now()) {
		if s.Tiered() && tierOf(sub) == TierPro {
			return HistoryPro
		}
		return HistoryPaid
	}
	return HistoryTrial
}

// UseQuota model chaqirilishidan oldin: sinovdagi mijozga kuniga TrialDailyLimit tadan
// ko'p javob tayyorlanmaydi. Ruxsat bo'lsa hisoblagich bittaga oshadi. To'langan obuna va
// adminlar cheklanmaydi. Hisob bazada (usage_daily) — qayta ishga tushish uni tozalamaydi.
// Baza xatosida yopiq (false), Entitled kabi.
func (s *Service) UseQuota(ctx context.Context, owner int64) bool {
	if !s.Enabled() || s.TrialDailyLimit <= 0 || s.Exempt[owner] {
		return true
	}
	if p, linked := s.payer(ctx, owner); linked {
		return s.UseQuota(ctx, p) // sinov limiti barcha akkauntlar uchun umumiy
	}
	sub, err := s.subscription(ctx, owner)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log().Error("billing quota", "owner", owner, "err", err)
		}
		return false
	}
	now := s.now()
	if sub.Premium(now) {
		return true
	}
	ok, err := s.Store.UseDaily(ctx, owner, now.In(quotaZone).Format(time.DateOnly), s.TrialDailyLimit)
	if err != nil {
		s.log().Error("billing quota", "owner", owner, "err", err)
		return false
	}
	return ok
}

// subscription obuna yozuvini qaytaradi. To'lov yoqilishidan OLDIN rozilik bergan
// mijozlarda yozuv bo'lmaydi — ularga ham (bir marta) sinov muddati beriladi,
// aks holda to'lov yoqilgan zahoti xizmat ogohlantirishsiz to'xtab qolardi.
func (s *Service) subscription(ctx context.Context, owner int64) (store.Subscription, error) {
	sub, err := s.Store.GetSubscription(ctx, owner)
	if !errors.Is(err, store.ErrNotFound) {
		return sub, err
	}
	acc, aerr := s.Store.GetAccount(ctx, owner)
	if aerr != nil || acc.ConsentAt == nil {
		return sub, err
	}
	if err := s.StartTrial(ctx, owner); err != nil {
		return sub, err
	}
	return s.Store.GetSubscription(ctx, owner)
}

// StartTrial birinchi marta (rozilik berilganda) bepul sinovni boshlaydi. Obuna yozuvi
// avval yaratilgan bo'lsa (akkaunt o'chirib qayta ochilgan bo'lsa ham) — hech narsa qilmaydi.
func (s *Service) StartTrial(ctx context.Context, owner int64) error {
	if !s.Enabled() {
		return nil
	}
	if _, linked := s.payer(ctx, owner); linked {
		return nil // qo'shimcha akkauntning o'z sinovi yo'q
	}
	if _, err := s.Store.GetSubscription(ctx, owner); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	days := s.TrialDays
	if _, err := s.Store.GetReferral(ctx, owner); err == nil && days > 0 {
		days += ReferralTrialBonus // do'st taklifi bilan kelgan — uzunroq sinov
	}
	sub := store.Subscription{OwnerID: owner}
	if days > 0 {
		sub.TrialUntil = s.now().Add(time.Duration(days) * 24 * time.Hour)
	}
	if err := s.Store.SaveSubscription(ctx, sub); err != nil {
		return err
	}
	s.Store.Audit(ctx, owner, "trial_started", strconv.Itoa(days))
	return nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// InvoiceLink tanlangan tarif uchun to'lov havolasini yaratadi (Mini App: Telegram.WebApp.openInvoice).
// Bitta tarif rejimida (ProPrice = 0) faqat TierAdvanced. Faol obunada shu tarifni qayta sotib
// olib bo'lmaydi (ikki marta to'lov), boshqa tarifga o'tish mumkin — qolgan kunlar o'tkaziladi.
func (s *Service) InvoiceLink(ctx context.Context, owner int64, tier Tier) (string, error) {
	if !s.Enabled() {
		return "", ErrDisabled
	}
	price := s.PriceOf(tier)
	if price <= 0 {
		return "", ErrUnknownTier
	}
	if sub, err := s.Store.GetSubscription(ctx, owner); err == nil &&
		sub.PaidUntil.After(s.now()) && !sub.Canceled && (!s.Tiered() || tierOf(sub) == tier) {
		return "", ErrAlreadySubscribed // ikki parallel obuna (ikki marta to'lov) bo'lmasin
	}
	payload, err := newPayload(owner, tier)
	if err != nil {
		return "", err
	}
	title, desc := "Kotib — oylik obuna", "AI kotib: shaxsiy xabarlaringizga javob qoralamalari. Har 30 kunda avtomatik yangilanadi, istalgan payt bekor qilish mumkin."
	if s.Tiered() {
		if tier == TierPro {
			title, desc = "Kotib Pro — oylik obuna", "Advanced'dagi hammasi + ovozli xabarlarga javob, Google Calendar/Contacts/Tasks, telefon avtomatikasi, eng uzun suhbat konteksti. Har 30 kunda avtomatik yangilanadi."
		} else {
			title = "Kotib Advanced — oylik obuna"
		}
	}
	return s.API.CreateInvoiceLink(ctx, InvoiceLink{
		Title:              title,
		Description:        desc,
		Payload:            payload,
		Label:              "30 kun",
		Amount:             price,
		SubscriptionPeriod: PeriodSeconds,
	})
}

// PreCheckout — to'lovdan oldingi tekshiruv. nil → to'lovga ruxsat.
// Qaytarilgan xato matni mijozga ko'rsatiladi.
func (s *Service) PreCheckout(ctx context.Context, from int64, currency string, amount int, payload string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	owner, tier, ok := parsePayload(payload)
	if !ok || owner != from {
		return errors.New("bu to'lov havolasi sizga tegishli emas")
	}
	want := s.Price // eski (tarifsiz) payload — Advanced narxi
	if tier != TierNone {
		want = s.PriceOf(tier)
	}
	if currency != Currency || want <= 0 || amount != want {
		return errors.New("narx o'zgargan — ilovani qayta ochib urinib ko'ring")
	}
	if _, err := s.Store.GetAccount(ctx, owner); err != nil {
		return errors.New("avval ilovada ro'yxatdan o'ting")
	}
	return nil
}

// PaymentInfo — successful_payment'dan kerakli maydonlar.
type PaymentInfo struct {
	Currency       string
	Amount         int
	Payload        string
	ChargeID       string
	Recurring      bool
	FirstRecurring bool
	ExpiresAt      time.Time // subscription_expiration_date (bo'lsa)
}

// PaymentReceived Telegram tasdiqlagan to'lovni yozadi va obunani uzaytiradi. Idempotent.
func (s *Service) PaymentReceived(ctx context.Context, from int64, p PaymentInfo) error {
	if p.Currency != Currency || p.Amount <= 0 || p.ChargeID == "" {
		return fmt.Errorf("kutilmagan to'lov: %s %d", p.Currency, p.Amount)
	}
	// Telegram pulni allaqachon yechgan — payload mos kelmasa ham to'lovchiga (from) yozamiz.
	if owner, ok := payloadOwner(p.Payload); !ok || owner != from {
		s.log().Warn("payload egasi mos emas", "from", from)
	}
	added, err := s.Store.AddPayment(ctx, store.Payment{
		ChargeID: p.ChargeID, OwnerID: from, Amount: p.Amount, Currency: p.Currency,
		Payload: p.Payload, Recurring: p.Recurring,
	})
	if err != nil {
		return err
	}
	if !added {
		return nil // takroriy update
	}
	sub, err := s.Store.GetSubscription(ctx, from)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	sub.OwnerID = from
	now := s.now()
	oldPaid, oldCharge, oldCanceled := sub.PaidUntil, sub.ChargeID, sub.Canceled
	// Yangi obuna (oylik yangilanish emas), eski to'langan muddat hali tugamagan — tarif almashtirildi.
	switching := !(p.Recurring && !p.FirstRecurring) && oldPaid.After(now) && oldCharge != "" && oldCharge != p.ChargeID
	if _, tier, ok := parsePayload(p.Payload); ok && tier != TierNone {
		sub.Tier = string(tier)
	}
	if !p.ExpiresAt.IsZero() {
		sub.PaidUntil = p.ExpiresAt
	} else {
		base := sub.PaidUntil
		if base.Before(now) {
			base = now
		}
		sub.PaidUntil = base.Add(Period)
	}
	// Bonus kunlar to'langan davr bilan ustma-ust kelmasin: qolgan qismi yangi davrdan keyinga suriladi
	// (Telegram har 30 kunda pul yechadi — aks holda bonus bekorga "yonib" ketardi).
	sub.BonusUntil = shiftBonus(sub.BonusUntil, laterOf(now, oldPaid), sub.PaidUntil)
	if switching {
		// Eski obunaning qolgan muddati narx nisbatida bonus kunlarga aylanadi (yangi davrdan keyin)
		// va uning avtomatik yangilanishi o'chiriladi — ikki obuna parallel pul yechmasin.
		credit := oldPaid.Sub(now)
		if old, err := s.Store.GetPayment(ctx, oldCharge); err == nil && old.Amount > 0 {
			credit = time.Duration(float64(credit) * float64(old.Amount) / float64(p.Amount))
		}
		sub.BonusUntil = laterOf(sub.BonusUntil, sub.PaidUntil).Add(credit.Truncate(time.Minute))
		if !oldCanceled {
			if err := s.API.EditUserStarSubscription(ctx, from, oldCharge, true); err != nil {
				s.log().Error("eski obunani bekor qilib bo'lmadi", "owner", from, "charge", oldCharge, "err", err)
			}
		}
		s.Store.Audit(ctx, from, "tier_switched", sub.Tier)
	}
	sub.ChargeID, sub.Canceled = p.ChargeID, false
	if err := s.Store.SaveSubscription(ctx, sub); err != nil {
		return err
	}
	s.Store.Audit(ctx, from, "payment", fmt.Sprintf("%d %s", p.Amount, p.Currency))
	s.referralPaid(ctx, from)
	return nil
}

// Cancel avtomatik uzaytirishni o'chiradi (to'langan muddat oxirigacha xizmat ishlaydi).
func (s *Service) Cancel(ctx context.Context, owner int64) error {
	return s.setCanceled(ctx, owner, true)
}

// Renew bot tomonidan bekor qilingan obunani qayta yoqadi.
func (s *Service) Renew(ctx context.Context, owner int64) error {
	return s.setCanceled(ctx, owner, false)
}

func (s *Service) setCanceled(ctx context.Context, owner int64, canceled bool) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	sub, err := s.Store.GetSubscription(ctx, owner)
	if err != nil || sub.ChargeID == "" || !sub.PaidUntil.After(s.now()) {
		return ErrNoSubscription
	}
	if sub.Canceled == canceled {
		return nil
	}
	if err := s.API.EditUserStarSubscription(ctx, owner, sub.ChargeID, canceled); err != nil {
		return err
	}
	sub.Canceled = canceled
	if err := s.Store.SaveSubscription(ctx, sub); err != nil {
		return err
	}
	event := "subscription_renewed"
	if canceled {
		event = "subscription_canceled"
	}
	s.Store.Audit(ctx, owner, event, "")
	return nil
}

// Refund (admin) to'lovni Telegram orqali qaytaradi va shu to'lov bergan muddatni bekor qiladi.
func (s *Service) Refund(ctx context.Context, chargeID string) (store.Payment, error) {
	p, err := s.Store.GetPayment(ctx, chargeID)
	if err != nil {
		return p, errors.New("to'lov topilmadi")
	}
	if p.Refunded {
		return p, errors.New("allaqachon qaytarilgan")
	}
	if err := s.API.RefundStarPayment(ctx, p.OwnerID, chargeID); err != nil {
		return p, err
	}
	return p, s.Refunded(ctx, p.OwnerID, chargeID)
}

// Refunded — to'lov qaytarilgani haqida (admin yoki Telegram'dan refunded_payment).
func (s *Service) Refunded(ctx context.Context, owner int64, chargeID string) error {
	if err := s.Store.MarkRefunded(ctx, chargeID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	sub, err := s.Store.GetSubscription(ctx, owner)
	if err == nil && sub.ChargeID == chargeID {
		now := s.now()
		// Qaytarilgan davr uchun xizmat yo'q; undan keyin turgan bonus kunlar esa saqlanadi.
		sub.BonusUntil = shiftBonus(sub.BonusUntil, laterOf(now, sub.PaidUntil), now)
		sub.PaidUntil, sub.Canceled = now, true
		if err := s.Store.SaveSubscription(ctx, sub); err != nil {
			return err
		}
	}
	s.Store.Audit(ctx, owner, "refunded", "")
	return nil
}

// RemindExpiring muddati 24 soat ichida tugaydigan va avtomatik yangilanmaydigan
// (sinov yoki bekor qilingan) obunalar egalariga bir marta eslatma yuboradi.
func (s *Service) RemindExpiring(ctx context.Context, notify Notifier) {
	if !s.Enabled() || notify == nil {
		return
	}
	now := s.now()
	subs, err := s.Store.ListSubscriptionsEnding(ctx, now, now.Add(remindBefore))
	if err != nil {
		s.log().Warn("billing reminders", "err", err)
		return
	}
	for _, sub := range subs {
		until := sub.Until()
		renewsAutomatically := sub.PaidUntil.Equal(until) && !sub.Canceled && sub.ChargeID != ""
		if renewsAutomatically || sub.RemindedFor.Equal(until) {
			continue
		}
		text := "Kotib obunangiz 24 soat ichida tugaydi. Davom ettirish uchun ilovada «Obuna» bo'limini oching."
		switch {
		case sub.BonusUntil.Equal(until):
			text = "Bonus kunlaringiz 24 soat ichida tugaydi. Kotib ishlashda davom etishi uchun ilovada obunani yoqing." + referralHint
		case sub.PaidUntil.Before(sub.TrialUntil) || sub.PaidUntil.IsZero():
			text = "Bepul sinov muddati 24 soat ichida tugaydi. Kotib ishlashda davom etishi uchun ilovada obunani yoqing." + referralHint
		}
		notify(ctx, sub.OwnerID, text, true)
		sub.RemindedFor = until
		if err := s.Store.SaveSubscription(ctx, sub); err != nil {
			s.log().Warn("billing reminder save", "owner", sub.OwnerID, "err", err)
		}
	}
}
