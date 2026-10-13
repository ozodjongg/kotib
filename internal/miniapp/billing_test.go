package miniapp

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

type fakeStarsAPI struct{ canceled int }

func (f *fakeStarsAPI) CreateInvoiceLink(context.Context, billing.InvoiceLink) (string, error) {
	return "https://t.me/$inv", nil
}
func (f *fakeStarsAPI) EditUserStarSubscription(_ context.Context, _ int64, _ string, c bool) error {
	if c {
		f.canceled++
	}
	return nil
}
func (f *fakeStarsAPI) RefundStarPayment(context.Context, int64, string) error { return nil }

func setupBilling(t *testing.T) (*httptest.Server, *store.Memory, *billing.Service, *fakeStarsAPI) {
	t.Helper()
	st := store.NewMemory()
	api := &fakeStarsAPI{}
	bill := &billing.Service{Store: st, API: api, Price: 100, TrialDays: 7}
	srv := newTestServer(t, &Server{BotToken: tok, Store: st, Ctl: &fakeCtl{}, Billing: bill})
	return srv, st, bill, api
}

func TestConsentStartsTrialAndInvoice(t *testing.T) {
	srv, _, _, _ := setupBilling(t)
	a := userAuth("20")
	if r, _ := do(t, "POST", srv.URL+"/api/billing/invoice", a, ""); r.StatusCode != 403 {
		t.Fatalf("rozilikdan oldin invoice bo'lmasligi kerak: %d", r.StatusCode)
	}
	do(t, "POST", srv.URL+"/api/consent", a, `{"accepted":true}`)
	_, me := do(t, "GET", srv.URL+"/api/me", a, "")
	b, _ := me["billing"].(map[string]any)
	if b["on_trial"] != true || b["days_left"] != float64(7) {
		t.Fatalf("sinov boshlanishi kerak: %v", b)
	}
	r, m := do(t, "POST", srv.URL+"/api/billing/invoice", a, "")
	if r.StatusCode != 200 || m["link"] != "https://t.me/$inv" {
		t.Fatalf("invoice: %d %v", r.StatusCode, m)
	}
}

func TestBillingCancelAndDeleteCancels(t *testing.T) {
	srv, st, bill, api := setupBilling(t)
	ctx := context.Background()
	a := userAuth("21")
	do(t, "POST", srv.URL+"/api/consent", a, `{"accepted":true}`)
	if r, _ := do(t, "POST", srv.URL+"/api/billing/cancel", a, ""); r.StatusCode != 409 {
		t.Fatalf("obunasiz bekor qilish 409: %d", r.StatusCode)
	}
	_ = bill.PaymentReceived(ctx, 21, billing.PaymentInfo{Currency: "XTR", Amount: 100, ChargeID: "c"})
	if r, _ := do(t, "POST", srv.URL+"/api/billing/invoice", a, ""); r.StatusCode != 409 {
		t.Fatalf("faol obunada ikkinchi invoice 409: %d", r.StatusCode)
	}
	do(t, "DELETE", srv.URL+"/api/account", a, `{"confirm":"DELETE"}`)
	if api.canceled != 1 {
		t.Fatal("akkaunt o'chirilganda obuna yangilanishi bekor qilinishi kerak")
	}
	// moliyaviy yozuv saqlanadi
	if sub, err := st.GetSubscription(ctx, 21); err != nil || !sub.PaidUntil.After(time.Now()) {
		t.Fatalf("obuna yozuvi saqlanishi kerak: %+v %v", sub, err)
	}
}

func TestActivityIsolated(t *testing.T) {
	srv, st, _, _ := setupBilling(t)
	st.Audit(context.Background(), 30, "paused", "")
	st.Audit(context.Background(), 31, "secret", "")
	req := httptestReq(t, srv.URL+"/api/activity", userAuth("30"))
	if len(req) != 1 || req[0]["event"] != "paused" {
		t.Fatalf("faqat o'z faolligi: %v", req)
	}
}

func TestAuthFailureLimiter(t *testing.T) {
	srv, _, _, _ := setupBilling(t)
	got429 := false
	for i := 0; i < 100; i++ {
		if r, _ := do(t, "GET", srv.URL+"/api/me", "tma bad", ""); r.StatusCode == 429 {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("ko'p yaroqsiz so'rovdan keyin 429 bo'lishi kerak")
	}
}
