package bot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
)

type recHandler struct {
	mu        sync.Mutex
	cmds      []string
	paid      []billing.PaymentInfo
	refunded  []string
	rejectPre bool
}

func (h *recHandler) Command(_ context.Context, _ int64, cmd, args string) Reply {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cmds = append(h.cmds, cmd+"|"+args)
	return Reply{Text: "ok"}
}
func (h *recHandler) PreCheckout(context.Context, int64, string, int, string) error {
	if h.rejectPre {
		return errors.New("yo'q")
	}
	return nil
}
func (h *recHandler) Paid(_ context.Context, _ int64, p billing.PaymentInfo) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.paid = append(h.paid, p)
}
func (h *recHandler) Refunded(_ context.Context, _ int64, id string) {
	h.refunded = append(h.refunded, id)
}

// fakeAPI bitta getUpdates javobini beradi, keyin bo'sh; barcha chaqiruvlarni yozib boradi.
func fakeAPI(t *testing.T, updates string) (*httptest.Server, *[]map[string]any, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var calls []map[string]any
	served := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		body["_method"] = method
		calls = append(calls, body)
		first := !served && method == "getUpdates"
		if first {
			served = true
		}
		mu.Unlock()
		switch {
		case first:
			w.Write([]byte(`{"ok":true,"result":` + updates + `}`))
		case method == "getUpdates":
			time.Sleep(20 * time.Millisecond)
			w.Write([]byte(`{"ok":true,"result":[]}`))
		case method == "createInvoiceLink":
			w.Write([]byte(`{"ok":true,"result":"https://t.me/$abc"}`))
		default:
			w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, &mu
}

func runBriefly(b *Bot) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	b.Run(ctx)
}

func TestDispatchPaymentsAndCommands(t *testing.T) {
	updates := `[
	 {"update_id":1,"pre_checkout_query":{"id":"q1","from":{"id":5},"currency":"XTR","total_amount":100,"invoice_payload":"p"}},
	 {"update_id":2,"message":{"from":{"id":5},"chat":{"id":5,"type":"private"},
	   "successful_payment":{"currency":"XTR","total_amount":100,"invoice_payload":"p","telegram_payment_charge_id":"ch1",
	   "is_recurring":true,"is_first_recurring":true,"subscription_expiration_date":1900000000,"provider_payment_charge_id":""}}},
	 {"update_id":3,"message":{"text":"/status@MyBot  hi ","from":{"id":5},"chat":{"id":5,"type":"private"}}},
	 {"update_id":4,"message":{"text":"/status","from":{"id":6},"chat":{"id":-100,"type":"group"}}},
	 {"update_id":5,"message":{"from":{"id":5},"chat":{"id":5,"type":"private"},"refunded_payment":{"telegram_payment_charge_id":"ch1"}}}
	]`
	srv, calls, mu := fakeAPI(t, updates)
	h := &recHandler{}
	b := New("TOKEN", "https://app.example/", slog.Default())
	b.BaseURL, b.Handler = srv.URL, h
	runBriefly(b)

	mu.Lock()
	defer mu.Unlock()
	var pre map[string]any
	for _, c := range *calls {
		if c["_method"] == "answerPreCheckoutQuery" {
			pre = c
		}
	}
	if pre == nil || pre["ok"] != true || pre["pre_checkout_query_id"] != "q1" {
		t.Fatalf("pre_checkout javobi: %v", pre)
	}
	if len(h.paid) != 1 || h.paid[0].ChargeID != "ch1" || !h.paid[0].Recurring || h.paid[0].ExpiresAt.Unix() != 1900000000 {
		t.Fatalf("paid: %+v", h.paid)
	}
	if len(h.cmds) != 1 || h.cmds[0] != "status|hi" {
		t.Fatalf("faqat shaxsiy chat buyrug'i: %v", h.cmds)
	}
	if len(h.refunded) != 1 {
		t.Fatal("refunded_payment qayta ishlanmadi")
	}
}

func TestPreCheckoutRejected(t *testing.T) {
	srv, calls, mu := fakeAPI(t, `[{"update_id":1,"pre_checkout_query":{"id":"q1","from":{"id":5},"currency":"XTR","total_amount":1,"invoice_payload":"p"}}]`)
	b := New("T", "", slog.Default())
	b.BaseURL, b.Handler = srv.URL, &recHandler{rejectPre: true}
	runBriefly(b)
	mu.Lock()
	defer mu.Unlock()
	for _, c := range *calls {
		if c["_method"] == "answerPreCheckoutQuery" {
			if c["ok"] != false || c["error_message"] != "yo'q" {
				t.Fatalf("rad etish: %v", c)
			}
			return
		}
	}
	t.Fatal("answerPreCheckoutQuery chaqirilmadi")
}

func TestCreateInvoiceLinkStarsParams(t *testing.T) {
	srv, calls, mu := fakeAPI(t, `[]`)
	b := New("T", "", slog.Default())
	b.BaseURL = srv.URL
	link, err := b.CreateInvoiceLink(context.Background(), billing.InvoiceLink{
		Title: "t", Description: "d", Payload: "p", Label: "l", Amount: 100, SubscriptionPeriod: billing.PeriodSeconds,
	})
	if err != nil || link != "https://t.me/$abc" {
		t.Fatal(link, err)
	}
	mu.Lock()
	defer mu.Unlock()
	c := (*calls)[0]
	if c["currency"] != "XTR" || c["subscription_period"] != float64(2592000) || c["provider_token"] != nil {
		t.Fatalf("Stars parametrlari: %v", c)
	}
}

func TestParseCommand(t *testing.T) {
	for in, want := range map[string]string{"/start": "start|", "/Refund@Bot abc ": "refund|abc", "salom": "", "/": ""} {
		c, a, ok := parseCommand(in)
		got := ""
		if ok {
			got = c + "|" + a
		}
		if got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
