package billing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

func newTiered(t *testing.T) (*Service, *store.Memory, *fakeAPI, *clock) {
	t.Helper()
	s, st, api, c := newSvc(t)
	s.Price, s.ProPrice = 150, 250
	return s, st, api, c
}

func pay(t *testing.T, s *Service, api *fakeAPI, tier Tier, charge string, expires time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.InvoiceLink(ctx, 1, tier); err != nil {
		t.Fatal(err)
	}
	inv := api.invoices[len(api.invoices)-1]
	if err := s.PreCheckout(ctx, 1, Currency, inv.Amount, inv.Payload); err != nil {
		t.Fatal(err)
	}
	if err := s.PaymentReceived(ctx, 1, PaymentInfo{Currency: Currency, Amount: inv.Amount, Payload: inv.Payload,
		ChargeID: charge, Recurring: true, FirstRecurring: true, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
}

func TestTierInvoices(t *testing.T) {
	s, _, api, _ := newTiered(t)
	ctx := context.Background()
	if _, err := s.InvoiceLink(ctx, 1, TierPro); err != nil {
		t.Fatal(err)
	}
	inv := api.invoices[0]
	if inv.Amount != 250 || !strings.HasPrefix(inv.Payload, "sub:1:pro:") || !strings.Contains(inv.Title, "Pro") {
		t.Fatalf("pro invoice: %+v", inv)
	}
	if s.PreCheckout(ctx, 1, Currency, 150, inv.Payload) == nil {
		t.Fatal("Pro payload Advanced narxida o'tmasligi kerak")
	}
	if _, err := s.InvoiceLink(ctx, 1, "gold"); !errors.Is(err, ErrUnknownTier) {
		t.Fatal(err)
	}
	if s.PreCheckout(ctx, 1, Currency, 250, "sub:1:gold:0123456789abcdef") == nil {
		t.Fatal("noma'lum tarif payload'i rad etilishi kerak")
	}
	one, _, _, _ := newSvc(t) // bitta tarif
	if _, err := one.InvoiceLink(ctx, 1, TierPro); !errors.Is(err, ErrUnknownTier) {
		t.Fatal("bitta tarifda Pro yo'q")
	}
}

func TestTierFeatures(t *testing.T) {
	s, _, api, c := newTiered(t)
	ctx := context.Background()
	_ = s.StartTrial(ctx, 1)
	if s.Tier(ctx, 1) != TierTrial || !s.HasPro(ctx, 1) || s.HistoryLimit(ctx, 1) != HistoryTrial {
		t.Fatal("sinovda Pro funksiyalari ochiq, kontekst qisqa")
	}
	pay(t, s, api, TierAdvanced, "a1", c.t.Add(30*day))
	if s.Tier(ctx, 1) != TierAdvanced || s.HasPro(ctx, 1) || s.HistoryLimit(ctx, 1) != HistoryPaid {
		t.Fatalf("advanced: %s %v %d", s.Tier(ctx, 1), s.HasPro(ctx, 1), s.HistoryLimit(ctx, 1))
	}
	st, _ := s.Status(ctx, 1)
	if st.Tier != TierAdvanced || st.HasPro || st.ProPrice != 250 {
		t.Fatalf("status: %+v", st)
	}
	if _, err := s.InvoiceLink(ctx, 1, TierAdvanced); !errors.Is(err, ErrAlreadySubscribed) {
		t.Fatal("o'sha tarifni qayta sotib olib bo'lmaydi")
	}
	c.t = c.t.Add(31 * day)
	if s.Tier(ctx, 1) != TierNone || s.HasPro(ctx, 1) {
		t.Fatal("obuna tugagach tarif yo'q")
	}
	s.Exempt = map[int64]bool{1: true}
	if !s.HasPro(ctx, 1) {
		t.Fatal("admin — Pro")
	}
}

func TestTierSwitchCreditsRemaining(t *testing.T) {
	s, _, api, c := newTiered(t)
	ctx := context.Background()
	_ = s.StartTrial(ctx, 1)
	start := c.t
	pay(t, s, api, TierAdvanced, "a1", start.Add(30*day))
	c.t = start.Add(10 * day) // 20 kun qoldi
	if _, err := s.InvoiceLink(ctx, 1, TierPro); err != nil {
		t.Fatalf("boshqa tarifga o'tish mumkin bo'lishi kerak: %v", err)
	}
	pay(t, s, api, TierPro, "p1", c.t.Add(30*day))
	sub, _ := s.Store.GetSubscription(ctx, 1)
	wantBonus := c.t.Add(30 * day).Add(12 * day) // 20 kun × 150/250
	if sub.Tier != "pro" || !sub.BonusUntil.Equal(wantBonus) || sub.ChargeID != "p1" {
		t.Fatalf("almashtirish: %+v (bonus kutilgan %v)", sub, wantBonus)
	}
	if len(api.edits) != 1 || !api.edits[0] {
		t.Fatalf("eski obuna bekor qilinishi kerak: %v", api.edits)
	}
	if !s.HasPro(ctx, 1) || s.HistoryLimit(ctx, 1) != HistoryPro {
		t.Fatal("Pro funksiyalari ochilishi kerak")
	}
	// Oylik yangilanish — almashtirish emas: bonus faqat suriladi, eski obuna tegilmaydi.
	c.t = c.t.Add(30 * day)
	_ = s.PaymentReceived(ctx, 1, PaymentInfo{Currency: Currency, Amount: 250, Payload: "sub:1:pro:0123456789abcdef",
		ChargeID: "p2", Recurring: true, ExpiresAt: c.t.Add(30 * day)})
	sub, _ = s.Store.GetSubscription(ctx, 1)
	if !sub.BonusUntil.Equal(c.t.Add(30*day).Add(12*day)) || len(api.edits) != 1 {
		t.Fatalf("yangilanish: %+v edits=%v", sub, api.edits)
	}
}

func TestLegacySubscriptionIsPro(t *testing.T) {
	s, _, _, c := newTiered(t)
	ctx := context.Background()
	// Tariflardan oldingi obunaning oylik yangilanishi eski payload bilan keladi.
	if err := s.PaymentReceived(ctx, 1, PaymentInfo{Currency: Currency, Amount: 100, Payload: "sub:1:0123456789abcdef",
		ChargeID: "old", Recurring: true, ExpiresAt: c.t.Add(30 * day)}); err != nil {
		t.Fatal(err)
	}
	if s.Tier(ctx, 1) != TierPro || !s.HasPro(ctx, 1) {
		t.Fatal("eski obunachilar funksiyalarini yo'qotmasligi kerak")
	}
	// Faqat bonus (hech to'lamagan) — Advanced.
	_ = s.AddBonusDays(ctx, 2, 7)
	if s.Tier(ctx, 2) != TierAdvanced || s.HasPro(ctx, 2) {
		t.Fatalf("bonus: %s", s.Tier(ctx, 2))
	}
}

func TestSingleTierAllFeatures(t *testing.T) {
	s, _, api, c := newSvc(t) // ProPrice = 0
	pay(t, s, api, TierAdvanced, "a1", c.t.Add(30*day))
	if !s.HasPro(context.Background(), 1) || s.HistoryLimit(context.Background(), 1) != HistoryPaid {
		t.Fatal("bitta tarifda obunada hamma funksiya ochiq bo'lishi kerak")
	}
}
