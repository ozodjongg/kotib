package billing

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

func charitySvc(t *testing.T) (*Service, *store.Memory, *fakeAPI, *clock) {
	t.Helper()
	s, st, api, c := newSvc(t)
	s.CharityPercent, s.CharityName = 10, "Mehr"
	c.t = time.Date(2026, 10, 13, 12, 0, 0, 0, quotaZone)
	return s, st, api, c
}

func addPay(t *testing.T, st *store.Memory, id string, amount int, payload string, at time.Time) {
	t.Helper()
	if ok, err := st.AddPayment(context.Background(), store.Payment{ChargeID: id, OwnerID: 1, Amount: amount, Currency: Currency,
		Payload: payload, CreatedAt: at}); !ok || err != nil {
		t.Fatal(ok, err)
	}
}

func TestCharityDisabled(t *testing.T) {
	s, _, _, _ := newSvc(t)
	if s.CharityEnabled() || s.CharityShare(100) != 0 {
		t.Fatal("CHARITY_PERCENT 0 — o'chiq")
	}
	if _, err := s.DonationLink(context.Background(), 1, 50); !errors.Is(err, ErrCharityDisabled) {
		t.Fatal(err)
	}
	if err := s.PreCheckout(context.Background(), 1, Currency, 50, "don:1:0123456789abcdef"); !errors.Is(err, ErrCharityDisabled) {
		t.Fatal(err)
	}
	if out := s.CloseCharityMonths(context.Background(), nil); out != nil {
		t.Fatal(out)
	}
}

func TestDonationFlow(t *testing.T) {
	s, st, api, _ := charitySvc(t)
	ctx := context.Background()
	if _, err := s.DonationLink(ctx, 7, 0); !errors.Is(err, ErrDonationAmount) {
		t.Fatal(err)
	}
	if _, err := s.DonationLink(ctx, 7, MaxPrice+1); !errors.Is(err, ErrDonationAmount) {
		t.Fatal(err)
	}
	if _, err := s.DonationLink(ctx, 7, 500); err != nil {
		t.Fatal(err)
	}
	in := api.invoices[len(api.invoices)-1]
	if in.SubscriptionPeriod != 0 || in.Amount != 500 || !IsDonation(in.Payload) || !strings.Contains(in.Description, "Mehr") {
		t.Fatalf("bir martalik xayriya invoice: %+v", in)
	}
	// Akkauntsiz (admin) ham xayriya qila oladi; boshqa odamning havolasi — yo'q.
	if err := s.PreCheckout(ctx, 7, Currency, 500, in.Payload); err != nil {
		t.Fatal(err)
	}
	if err := s.PreCheckout(ctx, 8, Currency, 500, in.Payload); err == nil {
		t.Fatal("boshqa egasining payload'i")
	}
	if err := s.PreCheckout(ctx, 7, "USD", 500, in.Payload); err == nil {
		t.Fatal("valyuta XTR bo'lishi kerak")
	}
	p := PaymentInfo{Currency: Currency, Amount: 500, Payload: in.Payload, ChargeID: "d1"}
	for range 2 { // takroriy update
		if err := s.PaymentReceived(ctx, 7, p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.GetSubscription(ctx, 7); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("xayriya obuna yaratmasligi kerak")
	}
	if pm, err := st.GetPayment(ctx, "d1"); err != nil || !pm.IsDonation() {
		t.Fatal(pm, err)
	}
	if stats, _ := st.Stats(ctx, time.Now()); stats.StarsTotal != 0 {
		t.Fatalf("xayriya obuna tushumiga qo'shilmaydi: %d", stats.StarsTotal)
	}
}

func TestCloseCharityMonths(t *testing.T) {
	s, st, _, c := charitySvc(t)
	s.Exempt = map[int64]bool{99: true}
	ctx := context.Background()
	oct := func(d int) time.Time { return time.Date(2026, 10, d, 10, 0, 0, 0, quotaZone) }
	addPay(t, st, "old", 1000, "sub:1:advanced:0123456789abcdef", time.Date(2026, 9, 30, 10, 0, 0, 0, quotaZone)) // hisobdan oldin
	addPay(t, st, "a", 155, "sub:1:advanced:0123456789abcdef", oct(2))
	addPay(t, st, "b", 155, "sub:1:advanced:0123456789abcdef", oct(20))
	addPay(t, st, "don", 40, "don:1:0123456789abcdef", oct(21))
	addPay(t, st, "r", 100, "sub:1:advanced:0123456789abcdef", oct(25))

	c.t = oct(28)
	if out := s.CloseCharityMonths(ctx, nil); len(out) != 0 {
		t.Fatalf("oy tugamagan — hisob yo'q: %v", out)
	}
	rep, err := s.CharityReport(ctx)
	if err != nil || rep.AccruingStars != 41+40 || rep.Percent != 10 || rep.Name != "Mehr" {
		t.Fatalf("joriy oy: %+v %v", rep, err)
	}

	c.t = time.Date(2026, 11, 1, 0, 30, 0, 0, quotaZone)
	var msgs []int64
	notify := func(_ context.Context, owner int64, text string, _ bool) {
		msgs = append(msgs, owner)
		if !strings.Contains(text, "2026-10") || !strings.Contains(text, "81 ⭐") {
			t.Errorf("xabar: %s", text)
		}
	}
	out := s.CloseCharityMonths(ctx, notify)
	// 410 × 10% = 41 (yig'ma hisob: 15+15+10 emas) + 40 xayriya.
	if len(out) != 1 || out[0].Period != "2026-10" || out[0].Stars != 81 || out[0].Share != 41 || out[0].Donated != 40 || out[0].Adjust != 0 {
		t.Fatalf("oktyabr: %+v", out)
	}
	if len(msgs) != 1 || msgs[0] != 99 {
		t.Fatalf("adminga xabar: %v", msgs)
	}
	if again := s.CloseCharityMonths(ctx, notify); len(again) != 0 || len(msgs) != 1 {
		t.Fatal("oy bir marta hisoblanadi")
	}

	// Noyabrda oktyabr to'lovi qaytarildi — dekabr boshida noyabr hisobidan ayiriladi.
	if err := st.MarkRefunded(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	addPay(t, st, "n", 300, "sub:1:advanced:0123456789abcdef", time.Date(2026, 11, 5, 10, 0, 0, 0, quotaZone))
	c.t = time.Date(2026, 12, 2, 9, 0, 0, 0, quotaZone)
	out = s.CloseCharityMonths(ctx, nil)
	if len(out) != 1 || out[0].Period != "2026-11" || out[0].Share != 30 || out[0].Stars != 20 || out[0].Adjust != -10 {
		t.Fatalf("noyabr (refund tuzatishi): %+v", out)
	}

	if err := s.MarkCharityPaid(ctx, out[0].ID, "", 99); err == nil {
		t.Fatal("isbotsiz belgilanmaydi")
	}
	if err := s.MarkCharityPaid(ctx, out[0].ID, "EQabc...tx", 99); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkCharityPaid(ctx, out[0].ID, "EQabc...tx", 99); err == nil {
		t.Fatal("ikki marta belgilanmaydi")
	}
	if _, err := s.AddManualCharity(ctx, 99, 0, "", ""); err == nil {
		t.Fatal("summa 0")
	}
	if _, err := s.AddManualCharity(ctx, 99, 1000, "Mehr fondiga naqd", ""); err != nil {
		t.Fatal(err)
	}
	rep, err = s.CharityReport(ctx)
	if err != nil || rep.PaidStars != 20+1000 || rep.PendingStars != 81 || rep.AccruingStars != 0 || len(rep.Payouts) != 3 {
		t.Fatalf("hisobot: %+v %v", rep, err)
	}
	if rep.Payouts[0].Kind != store.CharityManual || !rep.Payouts[0].Paid {
		t.Fatalf("yangisi birinchi: %+v", rep.Payouts[0])
	}
}

func TestCharityStartPersisted(t *testing.T) {
	s, st, _, c := charitySvc(t)
	ctx := context.Background()
	s.CloseCharityMonths(ctx, nil) // oktyabr boshlanish sifatida yoziladi
	c.t = time.Date(2027, 1, 10, 0, 0, 0, 0, quotaZone)
	s.CharityStartAt = time.Date(2026, 12, 1, 0, 0, 0, 0, quotaZone) // keyin o'zgartirilgan env hisobni siljitmaydi
	out := s.CloseCharityMonths(ctx, nil)
	if len(out) != 3 || out[0].Period != "2026-10" || out[2].Period != "2026-12" {
		t.Fatalf("oktyabr–dekabr: %+v", out)
	}
	if got, _ := st.CharityStart(ctx, time.Time{}); !got.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, quotaZone)) {
		t.Fatal(got)
	}
	if _, err := ParseCharityStart("2026/11"); err == nil {
		t.Fatal("format")
	}
}

func TestCharityPaidNotifiesSubscribers(t *testing.T) {
	s, st, _, c := charitySvc(t)
	ctx := context.Background()
	charityNotifyPause = 0
	s.CharityStartAt = time.Date(2026, 10, 1, 0, 0, 0, 0, quotaZone)
	for _, id := range []int64{2, 3, 4, 5} {
		_ = st.UpsertAccount(ctx, store.Account{OwnerID: id, Status: store.StatusActive})
	}
	_ = st.SetBan(ctx, store.Ban{OwnerID: 4, By: 99})
	var mu sync.Mutex
	got := map[int64]string{}
	s.Notify = func(_ context.Context, owner int64, text string, _ bool) {
		mu.Lock()
		defer mu.Unlock()
		got[owner] = text
	}
	oct := time.Date(2026, 10, 10, 10, 0, 0, 0, quotaZone)
	sub := "sub:1:advanced:0123456789abcdef"
	addPay(t, st, "s1", 150, sub, oct)                 // 1 → 15 ⭐
	addPay(t, st, "s1b", 150, sub, oct.Add(time.Hour)) // yana 1 → jami 30 ⭐
	_, _ = st.AddPayment(ctx, store.Payment{ChargeID: "s2", OwnerID: 2, Amount: 250, Currency: Currency, Payload: sub, CreatedAt: oct})
	_, _ = st.AddPayment(ctx, store.Payment{ChargeID: "d3", OwnerID: 3, Amount: 500, Currency: Currency, Payload: "don:3:0123456789abcdef", CreatedAt: oct}) // faqat donor
	_, _ = st.AddPayment(ctx, store.Payment{ChargeID: "s4", OwnerID: 4, Amount: 150, Currency: Currency, Payload: sub, CreatedAt: oct})                      // bloklangan
	_, _ = st.AddPayment(ctx, store.Payment{ChargeID: "s5", OwnerID: 5, Amount: 150, Currency: Currency, Payload: sub, CreatedAt: oct})
	_ = st.MarkRefunded(ctx, "s5")                                                                                                      // qaytarilgan
	_, _ = st.AddPayment(ctx, store.Payment{ChargeID: "s9", OwnerID: 9, Amount: 150, Currency: Currency, Payload: sub, CreatedAt: oct}) // akkaunt o'chirilgan
	_, _ = st.AddPayment(ctx, store.Payment{ChargeID: "n1", OwnerID: 1, Amount: 150, Currency: Currency, Payload: sub,
		CreatedAt: time.Date(2026, 11, 3, 0, 0, 0, 0, quotaZone)}) // boshqa oy

	c.t = time.Date(2026, 11, 20, 0, 0, 0, 0, quotaZone)
	if mine, err := s.MyCharity(ctx, 3); err != nil || mine != 500 {
		t.Fatalf("donor hissasi: %d %v", mine, err)
	}
	if mine, _ := s.MyCharity(ctx, 1); mine != 45 {
		t.Fatalf("obunachi hissasi (450 × 10%%): %d", mine)
	}
	out := s.CloseCharityMonths(ctx, nil)
	if len(out) != 1 {
		t.Fatal(out)
	}
	if err := s.MarkCharityPaid(ctx, out[0].ID, "https://tonviewer.com/transaction/abc", 99); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // ortiqcha xabar kelmasin
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("faqat obunachilar (1, 2): %v", got)
	}
	if !strings.Contains(got[1], "30 ⭐") || !strings.Contains(got[1], "oktabr 2026") || !strings.Contains(got[1], "tonviewer") || !strings.Contains(got[1], "Mehr") {
		t.Fatalf("1: %s", got[1])
	}
	if !strings.Contains(got[2], "25 ⭐") {
		t.Fatalf("2: %s", got[2])
	}
}

func TestManualCharityNoSubscriberNotice(t *testing.T) {
	s, _, _, _ := charitySvc(t)
	ctx := context.Background()
	called := make(chan struct{}, 1)
	s.Notify = func(context.Context, int64, string, bool) { called <- struct{}{} }
	if _, err := s.AddManualCharity(ctx, 99, 100, "naqd", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
		t.Fatal("qo'shimcha xayriya obunachilarga xabar yubormaydi")
	case <-time.After(50 * time.Millisecond):
	}
}
