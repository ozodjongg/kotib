package billing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

const day = 24 * time.Hour

// referrer=1 (newSvc'da akkaunti bor), kod yaratib, invitee'ni bog'laydi.
func attach(t *testing.T, s *Service, invitee int64) string {
	t.Helper()
	ctx := context.Background()
	if err := s.StartTrial(ctx, 1); err != nil {
		t.Fatal(err)
	}
	code, err := s.ReferralCode(ctx, 1)
	if err != nil || !ValidReferralCode(code) {
		t.Fatalf("kod: %q %v", code, err)
	}
	if !s.AttachReferral(ctx, invitee, code) {
		t.Fatal("bog'lanishi kerak")
	}
	return code
}

func TestReferralCodeStable(t *testing.T) {
	s, _, _, _ := newSvc(t)
	a, _ := s.ReferralCode(context.Background(), 1)
	b, _ := s.ReferralCode(context.Background(), 1)
	if a == "" || a != b {
		t.Fatalf("kod doimiy bo'lishi kerak: %q %q", a, b)
	}
	if got := ReferralLink("kotib_bot", a); got != "https://t.me/kotib_bot?start=ref_"+a {
		t.Fatal(got)
	}
	if ReferralLink("", a) != "" {
		t.Fatal("username yo'q — havola yo'q")
	}
}

func TestReferralAttachRules(t *testing.T) {
	s, st, _, _ := newSvc(t)
	ctx := context.Background()
	code := attach(t, s, 2)
	if s.AttachReferral(ctx, 2, code) {
		t.Fatal("ikkinchi marta bog'lanmaydi")
	}
	if s.AttachReferral(ctx, 1, code) {
		t.Fatal("o'zini taklif qilib bo'lmaydi")
	}
	for _, bad := range []string{"", "x", "AAAAAAAA", "aaaa'bbb", "aaaabbbbc"} {
		if s.AttachReferral(ctx, 3, bad) {
			t.Fatalf("yaroqsiz kod qabul qilindi: %q", bad)
		}
	}
	if s.AttachReferral(ctx, 3, "zzzzzzzz") {
		t.Fatal("noma'lum kod")
	}
	// Sinovni allaqachon olgan odam taklif qilingan bo'la olmaydi.
	_ = st.SaveSubscription(ctx, store.Subscription{OwnerID: 4})
	if s.AttachReferral(ctx, 4, code) {
		t.Fatal("eski foydalanuvchi bog'lanmasligi kerak")
	}
	// To'lov yoqilishidan oldin rozilik bergan eski foydalanuvchi ham.
	now := time.Now()
	_ = st.UpsertAccount(ctx, store.Account{OwnerID: 5, ConsentAt: &now})
	if s.AttachReferral(ctx, 5, code) {
		t.Fatal("rozilik bergan eski foydalanuvchi bog'lanmasligi kerak")
	}
	s.Exempt = map[int64]bool{6: true}
	if s.AttachReferral(ctx, 6, code) {
		t.Fatal("admin taklif qilingan bo'lmaydi")
	}
	off := &Service{Store: st}
	if off.AttachReferral(ctx, 7, code) {
		t.Fatal("to'lov o'chiq — referal yo'q")
	}
}

func TestReferralInviteeLongerTrial(t *testing.T) {
	s, _, _, c := newSvc(t)
	attach(t, s, 2)
	if err := s.StartTrial(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Status(context.Background(), 2)
	if !st.OnTrial || !st.TrialUntil.Equal(c.t.Add((7+ReferralTrialBonus)*day)) {
		t.Fatalf("taklif bilan sinov uzunroq bo'lishi kerak: %+v", st)
	}
}

func TestReferralRewardsOnceWithNotify(t *testing.T) {
	s, _, _, c := newSvc(t)
	ctx := context.Background()
	var notes []string
	s.Notify = func(_ context.Context, owner int64, text string, _ bool) {
		if owner == 1 {
			notes = append(notes, text)
		}
	}
	attach(t, s, 2)
	trialEnd := c.t.Add(7 * day)

	s.ReferralActivated(ctx, 2)
	s.ReferralActivated(ctx, 2) // takror — mukofot bir marta
	st, _ := s.Status(ctx, 1)
	if !st.Bonus || !st.BonusUntil.Equal(trialEnd.Add(ReferralActivatedDays*day)) {
		t.Fatalf("bonus sinovdan keyin davom etishi kerak: %+v", st)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "+7 kun") {
		t.Fatalf("xabar: %v", notes)
	}
	// Bonus olgan mijoz sinov cheklovlaridan ozod.
	s.TrialDailyLimit = 1
	if !s.UseQuota(ctx, 1) || !s.UseQuota(ctx, 1) || s.HistoryLimit(ctx, 1) != HistoryPaid {
		t.Fatal("bonusda cheklov bo'lmasligi kerak")
	}

	// Do'st to'lasa +30 kun (bir marta).
	pay := PaymentInfo{Currency: Currency, Amount: 100, Payload: "x", ChargeID: "c-2", ExpiresAt: c.t.Add(30 * day)}
	if err := s.PaymentReceived(ctx, 2, pay); err != nil {
		t.Fatal(err)
	}
	pay.ChargeID = "c-3" // keyingi oylik to'lov — referal mukofoti qayta berilmaydi
	_ = s.PaymentReceived(ctx, 2, pay)
	st, _ = s.Status(ctx, 1)
	if !st.BonusUntil.Equal(trialEnd.Add((ReferralActivatedDays + ReferralPaidDays) * day)) {
		t.Fatalf("to'lov mukofoti: %+v", st)
	}
	if len(notes) != 2 {
		t.Fatalf("xabarlar: %v", notes)
	}
	info, err := s.Referral(ctx, 1)
	if err != nil || info.Invited != 1 || info.Activated != 1 || info.Paid != 1 || info.EarnedDays != 37 {
		t.Fatalf("info: %+v %v", info, err)
	}

	// Muddat: sinov tugagach ham bonus hisobiga ishlaydi, keyin to'xtaydi.
	c.t = trialEnd.Add(time.Hour)
	if !s.Entitled(ctx, 1) {
		t.Fatal("bonusda xizmat ishlashi kerak")
	}
	c.t = trialEnd.Add(38 * day)
	if s.Entitled(ctx, 1) {
		t.Fatal("bonus tugagach xizmat to'xtashi kerak")
	}
}

func TestReferralActivatedCap(t *testing.T) {
	s, _, _, c := newSvc(t)
	ctx := context.Background()
	code := attach(t, s, 1000)
	s.ReferralActivated(ctx, 1000)
	for i := int64(1); i < ReferralActivatedCap+3; i++ {
		if !s.AttachReferral(ctx, 1000+i, code) {
			t.Fatal("bog'lanmadi")
		}
		s.ReferralActivated(ctx, 1000+i)
	}
	st, _ := s.Status(ctx, 1)
	want := c.t.Add(7 * day).Add(ReferralActivatedCap * ReferralActivatedDays * day)
	if !st.BonusUntil.Equal(want) {
		t.Fatalf("chegara: %v, kutilgan %v", st.BonusUntil, want)
	}
}

func TestBonusShiftsPastAutoRenewal(t *testing.T) {
	s, _, _, c := newSvc(t)
	ctx := context.Background()
	_ = s.StartTrial(ctx, 1)
	paid := c.t.Add(30 * day)
	if err := s.PaymentReceived(ctx, 1, PaymentInfo{Currency: Currency, Amount: 100, ChargeID: "p1", ExpiresAt: paid}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddBonusDays(ctx, 1, 7); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Status(ctx, 1)
	if !st.Paid || st.Bonus || !st.BonusUntil.Equal(paid.Add(7*day)) {
		t.Fatalf("bonus to'langan davrdan keyin turishi kerak: %+v", st)
	}
	// Telegram obunani yangiladi — bonus keyingi davrdan keyinga suriladi, yo'qolmaydi.
	c.t = paid
	paid2 := paid.Add(30 * day)
	_ = s.PaymentReceived(ctx, 1, PaymentInfo{Currency: Currency, Amount: 100, ChargeID: "p2", Recurring: true, ExpiresAt: paid2})
	st, _ = s.Status(ctx, 1)
	if !st.BonusUntil.Equal(paid2.Add(7 * day)) {
		t.Fatalf("bonus surilishi kerak: %v", st.BonusUntil)
	}
	// Qaytarilgan to'lov: bonus darhol boshlanadi (to'langan davr bekor).
	if err := s.Refunded(ctx, 1, "p2"); err != nil {
		t.Fatal(err)
	}
	st, _ = s.Status(ctx, 1)
	if st.Paid || !st.Bonus || !st.BonusUntil.Equal(c.t.Add(7*day)) {
		t.Fatalf("refunddan keyin bonus: %+v", st)
	}
}

func TestRemindBonusEnding(t *testing.T) {
	s, st, _, c := newSvc(t)
	ctx := context.Background()
	_ = st.SaveSubscription(ctx, store.Subscription{OwnerID: 1, TrialUntil: c.t.Add(-day), BonusUntil: c.t.Add(10 * time.Hour)})
	var got []string
	s.RemindExpiring(ctx, func(_ context.Context, _ int64, text string, _ bool) { got = append(got, text) })
	if len(got) != 1 || !strings.Contains(got[0], "Bonus") || !strings.Contains(got[0], "taklif") {
		t.Fatalf("eslatma: %v", got)
	}
}
