package billing

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// Do'stni taklif qilish (referal). Ikki tomonlama mukofot:
//   - taklif qilingan do'st: sinov ReferralTrialBonus kunga uzunroq;
//   - taklif qiluvchi: do'st Telegram akkauntini ulasa +ReferralActivatedDays (ko'pi bilan
//     ReferralActivatedCap marta — soxta akkauntlar bilan cheksiz bonus yig'ilmasin),
//     do'st birinchi marta to'lasa +ReferralPaidDays (cheklovsiz: bu haqiqiy daromad).
//
// Bonus kunlar to'langan obuna kabi to'liq xizmat (sinov cheklovlarisiz), Telegram esa pul yechmaydi.
// Firibgarlikdan himoya: o'zini taklif qilib bo'lmaydi; faqat hali obuna yozuvi (sinovi) yo'q odam
// taklif qilingan bo'la oladi; har odam bir marta; har bosqich mukofoti bir marta (atomar).
const (
	ReferralTrialBonus    = 7
	ReferralActivatedDays = 7
	ReferralPaidDays      = 30
	ReferralActivatedCap  = 10
	// ReferralStartPrefix — /start payload'i: t.me/<bot>?start=ref_<code>.
	ReferralStartPrefix = "ref_"
	referralCodeLen     = 8
)

const referralHint = "\n\nYoki do'stingizni taklif qiling — u ulansa sizga +7 kun bepul (ilova → Hisob → Do'stni taklif qilish)."

// ReferralInfo — Mini App va /taklif uchun.
type ReferralInfo struct {
	Enabled       bool   `json:"enabled"`
	Code          string `json:"code"`
	TrialBonus    int    `json:"trial_bonus"`    // do'stga qo'shimcha sinov kunlari
	TrialDays     int    `json:"trial_days"`     // do'stning jami sinovi
	ActivatedDays int    `json:"activated_days"` // do'st ulansa sizga
	PaidDays      int    `json:"paid_days"`      // do'st to'lasa sizga
	ActivatedCap  int    `json:"activated_cap"`
	store.ReferralStats
	EarnedDays int `json:"earned_days"`
}

// ReferralLink — do'stga yuboriladigan havola (bot username yoki kod bo'lmasa bo'sh).
func ReferralLink(botUsername, code string) string {
	if botUsername == "" || code == "" {
		return ""
	}
	return "https://t.me/" + botUsername + "?start=" + ReferralStartPrefix + code
}

// ReferralsEnabled — referal faqat to'lov yoqilganda ma'noga ega (bepul xizmatda mukofot yo'q).
func (s *Service) ReferralsEnabled() bool { return s.Enabled() }

const codeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789" // o'xshash belgilar (0/o, 1/l/i) yo'q

func newReferralCode() (string, error) {
	b := make([]byte, referralCodeLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b), nil
}

// ValidReferralCode — faqat bizning alifbo va uzunlik (bazaga tasodifiy matn bormasin).
func ValidReferralCode(code string) bool {
	if len(code) != referralCodeLen {
		return false
	}
	for _, r := range code {
		if !strings.ContainsRune(codeAlphabet, r) {
			return false
		}
	}
	return true
}

// ReferralCode egasining doimiy taklif kodini qaytaradi (birinchi chaqiruvda yaratiladi).
func (s *Service) ReferralCode(ctx context.Context, owner int64) (string, error) {
	if !s.ReferralsEnabled() {
		return "", ErrDisabled
	}
	for range 5 {
		c, err := newReferralCode()
		if err != nil {
			return "", err
		}
		code, err := s.Store.EnsureReferralCode(ctx, owner, c)
		if errors.Is(err, store.ErrNotFound) {
			continue // kod band — boshqasi
		}
		return code, err
	}
	return "", errors.New("taklif kodini yaratib bo'lmadi")
}

// Referral — egasining taklif kodi va natijalari.
func (s *Service) Referral(ctx context.Context, owner int64) (ReferralInfo, error) {
	info := ReferralInfo{
		Enabled: s.ReferralsEnabled(), TrialBonus: ReferralTrialBonus, TrialDays: s.TrialDays + ReferralTrialBonus,
		ActivatedDays: ReferralActivatedDays, PaidDays: ReferralPaidDays, ActivatedCap: ReferralActivatedCap,
	}
	if !info.Enabled {
		return info, nil
	}
	code, err := s.ReferralCode(ctx, owner)
	if err != nil {
		return info, err
	}
	st, err := s.Store.ReferralStats(ctx, owner)
	if err != nil {
		return info, err
	}
	info.Code, info.ReferralStats = code, st
	info.EarnedDays = min(st.Activated, ReferralActivatedCap)*ReferralActivatedDays + st.Paid*ReferralPaidDays
	return info, nil
}

// AttachReferral /start ref_<code> bilan kelgan foydalanuvchini taklif qiluvchiga bog'laydi.
// Faqat yangi foydalanuvchi (obuna yozuvi — ya'ni sinovi — hali yo'q) bog'lanadi. true — bog'landi.
func (s *Service) AttachReferral(ctx context.Context, invitee int64, code string) bool {
	if !s.ReferralsEnabled() || !ValidReferralCode(code) || s.Exempt[invitee] {
		return false
	}
	referrer, err := s.Store.ReferrerByCode(ctx, code)
	if err != nil || referrer == invitee {
		return false
	}
	if _, err := s.Store.GetSubscription(ctx, invitee); !errors.Is(err, store.ErrNotFound) {
		return false // avval sinovni olgan (yoki baza xatosi) — taklif hisoblanmaydi
	}
	if acc, err := s.Store.GetAccount(ctx, invitee); err == nil && acc.ConsentAt != nil {
		return false // to'lov yoqilishidan oldin ro'yxatdan o'tgan eski foydalanuvchi
	}
	added, err := s.Store.AddReferral(ctx, invitee, referrer)
	if err != nil {
		s.log().Warn("referral add", "invitee", invitee, "err", err)
		return false
	}
	return added
}

// ReferralActivated — taklif qilingan do'st Telegram akkauntini ulaganda (login muvaffaqiyatli).
func (s *Service) ReferralActivated(ctx context.Context, invitee int64) {
	s.referralReward(ctx, invitee, store.ReferralActivated)
}

func (s *Service) referralPaid(ctx context.Context, invitee int64) {
	s.referralReward(ctx, invitee, store.ReferralPaid)
}

func (s *Service) referralReward(ctx context.Context, invitee int64, stage store.ReferralStage) {
	if !s.ReferralsEnabled() {
		return
	}
	ref, err := s.Store.GetReferral(ctx, invitee)
	if err != nil {
		return // taklif bilan kelmagan
	}
	changed, err := s.Store.MarkReferral(ctx, invitee, stage, s.now())
	if err != nil || !changed {
		if err != nil {
			s.log().Warn("referral mark", "invitee", invitee, "err", err)
		}
		return
	}
	days, text := ReferralPaidDays, "🎉 Siz taklif qilgan do'stingiz Kotibga obuna bo'ldi! Sizga +%d kun bepul qo'shildi."
	if stage == store.ReferralActivated {
		st, err := s.Store.ReferralStats(ctx, ref.ReferrerID)
		if err != nil || st.Activated > ReferralActivatedCap {
			return // chegara: ulangan do'stlar uchun bonus ko'pi bilan ReferralActivatedCap marta
		}
		days, text = ReferralActivatedDays, "🎉 Siz taklif qilgan do'stingiz Kotibni uladi! Sizga +%d kun bepul qo'shildi."
	}
	if err := s.AddBonusDays(ctx, ref.ReferrerID, days); err != nil {
		s.log().Error("referral bonus", "referrer", ref.ReferrerID, "err", err)
		return
	}
	s.Store.Audit(ctx, ref.ReferrerID, "referral_bonus", fmt.Sprintf("%s +%d", stage, days))
	if s.Notify != nil {
		s.Notify(ctx, ref.ReferrerID, fmt.Sprintf(text, days), false)
	}
}

// AddBonusDays egasiga to'lovsiz to'liq xizmat kunlarini qo'shadi. Bonus joriy sinov yoki
// to'langan muddat tugagach davom etadi (ustma-ust kelib yo'qolmaydi); sinovdagi mijoz esa
// bonus olgan zahoti sinov cheklovlaridan ozod bo'ladi.
func (s *Service) AddBonusDays(ctx context.Context, owner int64, days int) error {
	if days <= 0 {
		return nil
	}
	sub, err := s.Store.GetSubscription(ctx, owner)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	sub.OwnerID = owner
	sub.BonusUntil = laterOf(s.now(), sub.Until()).Add(time.Duration(days) * 24 * time.Hour)
	return s.Store.SaveSubscription(ctx, sub)
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// shiftBonus bonusning from'dan keyingi qolgan qismini to'ga ko'chiradi (from'dan oldin tugagan
// yoki yo'q bonus o'zgarmaydi).
func shiftBonus(bonus, from, to time.Time) time.Time {
	if !bonus.After(from) {
		return bonus
	}
	return to.Add(bonus.Sub(from))
}
