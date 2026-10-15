package billing

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// Tier — mijozning hozirgi tarifi.
//
//   - advanced (Price): cheksiz javoblar, uzun kontekst, avto rejim, qoidalar, holatlar, xulosa, .ics;
//   - pro (ProPrice): + ovozli xabarlar, Google (Calendar/Contacts/Tasks), telefon avtomatikasi,
//     eng uzun kontekst.
//
// Sinov davrida Pro funksiyalari ochiq (mijoz tatib ko'rsin), lekin kunlik limit va qisqa kontekst bilan.
// ProPrice = 0 — bitta tarif: obunada hamma funksiya ochiq (tariflardan oldingi xulq).
type Tier string

const (
	TierNone     Tier = ""
	TierTrial    Tier = "trial"
	TierAdvanced Tier = "advanced"
	TierPro      Tier = "pro"
)

// HistoryPro — Pro tarifda modelga beriladigan oxirgi xabarlar soni.
const HistoryPro = 40

// ErrUnknownTier — noma'lum yoki yoqilmagan tarif.
var ErrUnknownTier = errors.New("bunday tarif yo'q")

// Tiered — ikki tarif yoqilganmi.
func (s *Service) Tiered() bool { return s.Enabled() && s.ProPrice > 0 }

// PriceOf tarif narxi (Stars); noma'lum/yoqilmagan tarif uchun 0.
func (s *Service) PriceOf(t Tier) int {
	switch {
	case t == TierAdvanced:
		return s.Price
	case t == TierPro && s.Tiered():
		return s.ProPrice
	}
	return 0
}

// tierOf — obuna yozuvidagi to'langan tarif. Bo'sh: avval to'lagan bo'lsa — tariflardan oldingi
// (eski) obuna, Pro deb hisoblanadi; hech to'lamagan (faqat bonus) — Advanced.
func tierOf(sub store.Subscription) Tier {
	switch Tier(sub.Tier) {
	case TierAdvanced, TierPro:
		return Tier(sub.Tier)
	}
	if !sub.PaidUntil.IsZero() {
		return TierPro
	}
	return TierAdvanced
}

// TierAt — now paytidagi tarif (obuna yozuvidan).
func (s *Service) tierAt(sub store.Subscription, now time.Time) Tier {
	switch {
	case sub.Premium(now):
		return tierOf(sub)
	case sub.TrialUntil.After(now):
		return TierTrial
	}
	return TierNone
}

// Tier — mijozning hozirgi tarifi. To'lov o'chiq yoki admin — Pro.
func (s *Service) Tier(ctx context.Context, owner int64) Tier {
	if !s.Enabled() || s.Exempt[owner] {
		return TierPro
	}
	sub, err := s.subscription(ctx, owner)
	if err != nil {
		return TierNone
	}
	return s.tierAt(sub, s.now())
}

// HasPro — Pro funksiyalari (ovoz, Google, avtomatika) ochiqmi: Pro tarif, sinov, admin,
// to'lov o'chiq yoki bitta tarif (ProPrice = 0) bo'lsa — faol obunada.
func (s *Service) HasPro(ctx context.Context, owner int64) bool {
	switch s.Tier(ctx, owner) {
	case TierPro, TierTrial:
		return true
	case TierAdvanced:
		return !s.Tiered()
	}
	return false
}

// newPayload — "sub:<owner>:<tier>:<nonce>".
func newPayload(owner int64, tier Tier) (string, error) {
	nonce, err := randomHex(8)
	if err != nil {
		return "", err
	}
	return payloadPrefix + strconv.FormatInt(owner, 10) + ":" + string(tier) + ":" + nonce, nil
}

// parsePayload payload'dan ega va tarifni ajratadi. Eski format "sub:<owner>:<nonce>" — tarif bo'sh
// (tariflardan oldingi obunaning oylik yangilanishi shu payload bilan keladi).
func parsePayload(payload string) (int64, Tier, bool) {
	rest, ok := strings.CutPrefix(payload, payloadPrefix)
	if !ok {
		return 0, "", false
	}
	parts := strings.Split(rest, ":")
	var tier Tier
	switch len(parts) {
	case 2:
	case 3:
		tier = Tier(parts[1])
		if tier != TierAdvanced && tier != TierPro {
			return 0, "", false
		}
	default:
		return 0, "", false
	}
	if len(parts[len(parts)-1]) != 16 {
		return 0, "", false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	return id, tier, err == nil && id > 0
}

// payloadOwner payload'dan egani ajratadi.
func payloadOwner(payload string) (int64, bool) {
	id, _, ok := parsePayload(payload)
	return id, ok
}
