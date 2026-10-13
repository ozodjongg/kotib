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

type fakeAPI struct {
	mu       sync.Mutex
	invoices []InvoiceLink
	edits    []bool
	refunds  []string
}

func (f *fakeAPI) CreateInvoiceLink(_ context.Context, in InvoiceLink) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invoices = append(f.invoices, in)
	return "https://t.me/$invoice", nil
}
func (f *fakeAPI) EditUserStarSubscription(_ context.Context, _ int64, _ string, c bool) error {
	f.edits = append(f.edits, c)
	return nil
}
func (f *fakeAPI) RefundStarPayment(_ context.Context, _ int64, id string) error {
	f.refunds = append(f.refunds, id)
	return nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newSvc(t *testing.T) (*Service, *store.Memory, *fakeAPI, *clock) {
	t.Helper()
	st := store.NewMemory()
	api := &fakeAPI{}
	c := &clock{t: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	_ = st.UpsertAccount(context.Background(), store.Account{OwnerID: 1, Status: store.StatusActive})
	return &Service{Store: st, API: api, Price: 100, TrialDays: 7, Now: c.now}, st, api, c
}

func TestDisabledMeansFree(t *testing.T) {
	s := &Service{Store: store.NewMemory()}
	if !s.Entitled(context.Background(), 1) {
		t.Fatal("narx 0 bo'lsa xizmat bepul bo'lishi kerak")
	}
	if _, err := s.InvoiceLink(context.Background(), 1); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
}

func TestTrialOnceAndExpiry(t *testing.T) {
	s, st, _, c := newSvc(t)
	ctx := context.Background()
	if s.Entitled(ctx, 1) {
		t.Fatal("sinovsiz ruxsat bo'lmasligi kerak")
	}
	_ = s.StartTrial(ctx, 1)
	if !s.Entitled(ctx, 1) {
		t.Fatal("sinov paytida ruxsat bo'lishi kerak")
	}
	c.t = c.t.Add(8 * 24 * time.Hour)
	if s.Entitled(ctx, 1) {
		t.Fatal("sinov tugagach ruxsat bo'lmasligi kerak")
	}
	// Akkauntni o'chirib qayta ochish yangi sinov bermaydi.
	_ = st.DeleteAccount(ctx, 1)
	_ = s.StartTrial(ctx, 1)
	if s.Entitled(ctx, 1) {
		t.Fatal("sinov qayta berilmasligi kerak")
	}
}

func TestPayloadAndPreCheckout(t *testing.T) {
	s, _, api, _ := newSvc(t)
	ctx := context.Background()
	if _, err := s.InvoiceLink(ctx, 1); err != nil {
		t.Fatal(err)
	}
	inv := api.invoices[0]
	if inv.Amount != 100 || inv.SubscriptionPeriod != PeriodSeconds || !strings.HasPrefix(inv.Payload, "sub:1:") {
		t.Fatalf("invoice: %+v", inv)
	}
	if err := s.PreCheckout(ctx, 1, Currency, 100, inv.Payload); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		from    int64
		cur     string
		amount  int
		payload string
	}{
		{2, Currency, 100, inv.Payload},   // boshqa odam
		{1, "USD", 100, inv.Payload},      // valyuta
		{1, Currency, 1, inv.Payload},     // summa
		{1, Currency, 100, "sub:1:short"}, // soxta nonce
		{1, Currency, 100, "anything"},    // format
	} {
		if s.PreCheckout(ctx, c.from, c.cur, c.amount, c.payload) == nil {
			t.Errorf("rad etilishi kerak edi: %+v", c)
		}
	}
}

func TestPaymentIdempotentAndExtends(t *testing.T) {
	s, _, _, c := newSvc(t)
	ctx := context.Background()
	p := PaymentInfo{Currency: Currency, Amount: 100, ChargeID: "ch1", Payload: "sub:1:0123456789abcdef"}
	for i := 0; i < 2; i++ { // ikki marta kelgan update bir marta hisoblanadi
		if err := s.PaymentReceived(ctx, 1, p); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := s.Status(ctx, 1)
	want := c.t.Add(Period)
	if !st.Paid || st.PaidUntil == nil || !st.PaidUntil.Equal(want) {
		t.Fatalf("status: %+v", st)
	}
	// Yangilanish (recurring) Telegram bergan muddatni o'rnatadi.
	exp := c.t.Add(60 * 24 * time.Hour)
	_ = s.PaymentReceived(ctx, 1, PaymentInfo{Currency: Currency, Amount: 100, ChargeID: "ch2", Recurring: true, ExpiresAt: exp})
	if st, _ := s.Status(ctx, 1); !st.PaidUntil.Equal(exp) {
		t.Fatalf("recurring: %v", st.PaidUntil)
	}
	if err := s.PaymentReceived(ctx, 1, PaymentInfo{Currency: "USD", Amount: 1, ChargeID: "x"}); err == nil {
		t.Fatal("XTR bo'lmagan to'lov rad etilishi kerak")
	}
}

func TestNoDoubleSubscription(t *testing.T) {
	s, _, _, _ := newSvc(t)
	ctx := context.Background()
	_ = s.PaymentReceived(ctx, 1, PaymentInfo{Currency: Currency, Amount: 100, ChargeID: "ch1"})
	if _, err := s.InvoiceLink(ctx, 1); !errors.Is(err, ErrAlreadySubscribed) {
		t.Fatalf("faol obunada yangi invoice bo'lmasligi kerak: %v", err)
	}
	_ = s.Cancel(ctx, 1)
	if _, err := s.InvoiceLink(ctx, 1); err != nil {
		t.Fatalf("bekor qilingandan keyin qayta obuna bo'lish mumkin: %v", err)
	}
}

func TestCancelRenewRefund(t *testing.T) {
	s, _, api, c := newSvc(t)
	ctx := context.Background()
	if err := s.Cancel(ctx, 1); !errors.Is(err, ErrNoSubscription) {
		t.Fatal(err)
	}
	_ = s.PaymentReceived(ctx, 1, PaymentInfo{Currency: Currency, Amount: 100, ChargeID: "ch1"})
	_ = s.Cancel(ctx, 1)
	st, _ := s.Status(ctx, 1)
	if !st.Canceled || !st.Active || !st.Renewable {
		t.Fatalf("bekor qilingan obuna muddat oxirigacha ishlashi kerak: %+v", st)
	}
	_ = s.Renew(ctx, 1)
	if len(api.edits) != 2 || api.edits[0] != true || api.edits[1] != false {
		t.Fatalf("edits: %v", api.edits)
	}
	if _, err := s.Refund(ctx, "ch1"); err != nil {
		t.Fatal(err)
	}
	if s.Entitled(ctx, 1) {
		t.Fatal("qaytarilgan to'lovdan keyin ruxsat bo'lmasligi kerak")
	}
	if _, err := s.Refund(ctx, "ch1"); err == nil {
		t.Fatal("ikki marta qaytarib bo'lmaydi")
	}
	_ = c
}

func TestRemindExpiringOnce(t *testing.T) {
	s, _, _, c := newSvc(t)
	ctx := context.Background()
	_ = s.StartTrial(ctx, 1)
	c.t = c.t.Add(6*24*time.Hour + 12*time.Hour) // 12 soat qoldi
	var sent int
	notify := func(context.Context, int64, string, bool) { sent++ }
	s.RemindExpiring(ctx, notify)
	s.RemindExpiring(ctx, notify)
	if sent != 1 {
		t.Fatalf("eslatma bir marta bo'lishi kerak: %d", sent)
	}
}

func TestExistingConsentedUserGetsTrial(t *testing.T) {
	s, st, _, _ := newSvc(t)
	ctx := context.Background()
	now := time.Now()
	_ = st.UpsertAccount(ctx, store.Account{OwnerID: 5, Status: store.StatusActive, ConsentAt: &now})
	if !s.Entitled(ctx, 5) {
		t.Fatal("to'lov yoqilishidan oldin rozilik bergan mijoz sinov olishi kerak")
	}
	if s.Entitled(ctx, 1) {
		t.Fatal("rozilik bermagan mijozga sinov berilmasligi kerak")
	}
}

func TestExemptAdmins(t *testing.T) {
	s, _, _, _ := newSvc(t)
	s.Exempt = map[int64]bool{42: true}
	if !s.Entitled(context.Background(), 42) {
		t.Fatal("admin to'lovdan ozod bo'lishi kerak")
	}
	if st, _ := s.Status(context.Background(), 42); !st.Exempt || !st.Active {
		t.Fatalf("status: %+v", st)
	}
}
