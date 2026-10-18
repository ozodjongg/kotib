package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// Admin amallari: bloklash tekshiruvi va sovg'a (tekin) obuna.

// MaxGrantDays — bir martada beriladigan sovg'a obunaning yuqori chegarasi (~10 yil, amalda muddatsiz).
const MaxGrantDays = 3650

// Banned — egasi admin tomonidan bloklanganmi. Baza xatosida false (xato logga yoziladi):
// vaqtinchalik nosozlik hamma mijozlarni to'xtatib qo'ymasin.
func (s *Service) Banned(ctx context.Context, owner int64) bool {
	if s == nil || s.Store == nil {
		return false
	}
	_, err := s.Store.GetBan(ctx, owner)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log().Error("ban check", "owner", owner, "err", err)
	}
	return err == nil
}

// Grant egasiga days kunlik to'lovsiz obuna beradi (tier: Advanced yoki Pro). Sovg'a joriy
// sinov/to'langan muddatdan keyin davom etadi (AddBonusDays kabi); Pro sovg'asi tarifni Pro qiladi.
func (s *Service) Grant(ctx context.Context, owner int64, days int, tier Tier) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	if days < 1 || days > MaxGrantDays {
		return fmt.Errorf("kunlar 1–%d bo'lishi kerak", MaxGrantDays)
	}
	if tier != TierAdvanced && tier != TierPro {
		return ErrUnknownTier
	}
	sub, err := s.Store.GetSubscription(ctx, owner)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	sub.OwnerID = owner
	sub.BonusUntil = laterOf(s.now(), sub.Until()).Add(time.Duration(days) * 24 * time.Hour)
	if tier == TierPro && s.Tiered() {
		sub.Tier = string(TierPro)
	}
	return s.Store.SaveSubscription(ctx, sub)
}

// RevokeGrant sovg'a/bonus kunlarini darhol tugatadi (to'langan muddat va sinov o'zgarmaydi).
func (s *Service) RevokeGrant(ctx context.Context, owner int64) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	sub, err := s.Store.GetSubscription(ctx, owner)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNoSubscription
	}
	if err != nil {
		return err
	}
	now := s.now()
	if !sub.BonusUntil.After(now) {
		return ErrNoSubscription
	}
	sub.BonusUntil = now
	if !sub.PaidUntil.After(now) {
		sub.Tier = "" // to'lanmagan sovg'a Pro'si bilan birga tugaydi
	}
	return s.Store.SaveSubscription(ctx, sub)
}
