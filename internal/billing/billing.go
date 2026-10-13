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
	"strings"
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
	Store     store.Store
	API       API
	Price     int // oyiga Stars; 0 → to'lov o'chiq, xizmat hammaga bepul
	TrialDays int
	Exempt    map[int64]bool // to'lovdan ozod (ADMIN_IDS) — o'z botingizni bepul ishlatish/sinash uchun
	Log       *slog.Logger
	Now       func() time.Time
}

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
	Price      int        `json:"price"`
	TrialDays  int        `json:"trial_days"`
	Active     bool       `json:"active"`      // hozir xizmatdan foydalanish mumkinmi
	Exempt     bool       `json:"exempt"`      // admin: to'lovdan ozod
	OnTrial    bool       `json:"on_trial"`    // faqat sinov muddati hisobiga
	Paid       bool       `json:"paid"`        // to'langan davr ichida
	Canceled   bool       `json:"canceled"`    // avtomatik uzaytirish o'chirilgan
	Renewable  bool       `json:"renewable"`   // bekor qilingan obunani qayta yoqish mumkin
	TrialUntil *time.Time `json:"trial_until"` // nil — sinov boshlanmagan
	PaidUntil  *time.Time `json:"paid_until"`
	Until      *time.Time `json:"until"`
	DaysLeft   int        `json:"days_left"`
}

func ptr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (s *Service) Status(ctx context.Context, owner int64) (Status, error) {
	if !s.Enabled() {
		return Status{Active: true}, nil
	}
	st := Status{Enabled: true, Price: s.Price, TrialDays: s.TrialDays}
	if s.Exempt[owner] {
		st.Active, st.Exempt = true, true
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
	st.Paid = sub.PaidUntil.After(now)
	st.Active = sub.Until().After(now)
	st.OnTrial = st.Active && !st.Paid
	st.Canceled = sub.Canceled
	st.Renewable = st.Paid && sub.Canceled && sub.ChargeID != ""
	if st.Active {
		st.DaysLeft = int((sub.Until().Sub(now) + 24*time.Hour - time.Second) / (24 * time.Hour))
	}
	return st, nil
}

// Entitled — AI javob tayyorlashi mumkinmi. Baza xatosida yopiq (false): to'lanmagan
// xizmat ko'rsatilmasin; xato logga yoziladi.
func (s *Service) Entitled(ctx context.Context, owner int64) bool {
	if !s.Enabled() || s.Exempt[owner] {
		return true
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
	if _, err := s.Store.GetSubscription(ctx, owner); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	sub := store.Subscription{OwnerID: owner}
	if s.TrialDays > 0 {
		sub.TrialUntil = s.now().Add(time.Duration(s.TrialDays) * 24 * time.Hour)
	}
	if err := s.Store.SaveSubscription(ctx, sub); err != nil {
		return err
	}
	s.Store.Audit(ctx, owner, "trial_started", strconv.Itoa(s.TrialDays))
	return nil
}

func newPayload(owner int64) (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return payloadPrefix + strconv.FormatInt(owner, 10) + ":" + hex.EncodeToString(b), nil
}

// payloadOwner payload'dan egani ajratadi ("sub:<owner>:<nonce>").
func payloadOwner(payload string) (int64, bool) {
	rest, ok := strings.CutPrefix(payload, payloadPrefix)
	if !ok {
		return 0, false
	}
	idStr, nonce, ok := strings.Cut(rest, ":")
	if !ok || len(nonce) != 16 {
		return 0, false
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	return id, err == nil && id > 0
}

// InvoiceLink obuna uchun to'lov havolasini yaratadi (Mini App: Telegram.WebApp.openInvoice).
func (s *Service) InvoiceLink(ctx context.Context, owner int64) (string, error) {
	if !s.Enabled() {
		return "", ErrDisabled
	}
	if sub, err := s.Store.GetSubscription(ctx, owner); err == nil &&
		sub.PaidUntil.After(s.now()) && !sub.Canceled {
		return "", ErrAlreadySubscribed // ikki parallel obuna (ikki marta to'lov) bo'lmasin
	}
	payload, err := newPayload(owner)
	if err != nil {
		return "", err
	}
	return s.API.CreateInvoiceLink(ctx, InvoiceLink{
		Title:              "Kotib — oylik obuna",
		Description:        "AI kotib: shaxsiy xabarlaringizga javob qoralamalari. Har 30 kunda avtomatik yangilanadi, istalgan payt bekor qilish mumkin.",
		Payload:            payload,
		Label:              "30 kun",
		Amount:             s.Price,
		SubscriptionPeriod: PeriodSeconds,
	})
}

// PreCheckout — to'lovdan oldingi tekshiruv. nil → to'lovga ruxsat.
// Qaytarilgan xato matni mijozga ko'rsatiladi.
func (s *Service) PreCheckout(ctx context.Context, from int64, currency string, amount int, payload string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	owner, ok := payloadOwner(payload)
	if !ok || owner != from {
		return errors.New("bu to'lov havolasi sizga tegishli emas")
	}
	if currency != Currency || amount != s.Price {
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
	if !p.ExpiresAt.IsZero() {
		sub.PaidUntil = p.ExpiresAt
	} else {
		base := sub.PaidUntil
		if base.Before(now) {
			base = now
		}
		sub.PaidUntil = base.Add(Period)
	}
	sub.ChargeID, sub.Canceled = p.ChargeID, false
	if err := s.Store.SaveSubscription(ctx, sub); err != nil {
		return err
	}
	s.Store.Audit(ctx, from, "payment", fmt.Sprintf("%d %s", p.Amount, p.Currency))
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
		sub.PaidUntil, sub.Canceled = s.now(), true // qaytarilgan davr uchun xizmat yo'q
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
		if sub.PaidUntil.Before(sub.TrialUntil) || sub.PaidUntil.IsZero() {
			text = "Bepul sinov muddati 24 soat ichida tugaydi. Kotib ishlashda davom etishi uchun ilovada obunani yoqing."
		}
		notify(ctx, sub.OwnerID, text, true)
		sub.RemindedFor = until
		if err := s.Store.SaveSubscription(ctx, sub); err != nil {
			s.log().Warn("billing reminder save", "owner", sub.OwnerID, "err", err)
		}
	}
}
