package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestCallbackAnsweredAndEdited(t *testing.T) {
	updates := `[
	 {"update_id":1,"callback_query":{"id":"cb1","from":{"id":5},"data":"act:1:y","message":{"message_id":9,"text":"x","chat":{"id":5,"type":"private"}}}},
	 {"update_id":2,"callback_query":{"id":"cb2","from":{"id":6},"data":"act:1:y","message":{"message_id":3,"text":"x","chat":{"id":-100,"type":"group"}}}}
	]`
	srv, calls, mu := fakeAPI(t, updates)
	h := &recHandler{}
	b := New("TOKEN", "https://app.example/", slog.Default())
	b.BaseURL, b.Handler = srv.URL, h
	runBriefly(b)
	mu.Lock()
	defer mu.Unlock()
	if len(h.callbacks) != 1 || h.callbacks[0] != "5|act:1:y" {
		t.Fatalf("faqat shaxsiy chatdagi tugma: %v", h.callbacks)
	}
	var answers, edits int
	for _, c := range *calls {
		switch c["_method"] {
		case "answerCallbackQuery":
			answers++
		case "editMessageText":
			edits++
			if c["message_id"] != float64(9) || c["text"] != "yangi matn" || !strings.Contains(fmt.Sprint(c["reply_markup"]), "a.ics") {
				t.Fatalf("edit: %v", c)
			}
		}
	}
	if answers != 2 || edits != 1 {
		t.Fatalf("answers=%d edits=%d", answers, edits)
	}
}

type recHandler struct {
	mu        sync.Mutex
	cmds      []string
	paid      []billing.PaymentInfo
	refunded  []string
	rejectPre bool
	callbacks []string
	replies   []string
}

func TestReplyToBotMessage(t *testing.T) {
	updates := `[
	 {"update_id":1,"message":{"text":"Ertaga gaplashamiz","from":{"id":5},"chat":{"id":5,"type":"private"},
	   "reply_to_message":{"message_id":42,"from":{"id":1},"reply_markup":{"inline_keyboard":[[{"text":"Yuborish","callback_data":"d:7:s"}]]}}}},
	 {"update_id":2,"message":{"text":"oddiy matn","from":{"id":5},"chat":{"id":5,"type":"private"}}}
	]`
	srv, calls, mu := fakeAPI(t, updates)
	h := &recHandler{}
	b := New("TOKEN", "https://app.example/", slog.Default())
	b.BaseURL, b.Handler = srv.URL, h
	runBriefly(b)
	mu.Lock()
	defer mu.Unlock()
	if len(h.replies) != 1 || h.replies[0] != "5|d:7:s|Ertaga gaplashamiz" {
		t.Fatalf("replies: %v", h.replies)
	}
	// Buyruqsiz oddiy matn — avtomatik /sor.
	if len(h.cmds) != 1 || h.cmds[0] != "kotib|oddiy matn" {
		t.Fatalf("cmds: %v", h.cmds)
	}
	var edited, sent bool
	for _, c := range *calls {
		if c["_method"] == "editMessageText" && c["message_id"] == float64(42) {
			edited = true
		}
		if c["_method"] == "sendMessage" && c["text"] == "Yuborildi" {
			sent = true
		}
	}
	if !edited || !sent {
		t.Fatalf("edited=%v sent=%v", edited, sent)
	}
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
func (h *recHandler) Callback(_ context.Context, from int64, data string) Answer {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.callbacks = append(h.callbacks, fmt.Sprintf("%d|%s", from, data))
	return Answer{Toast: "OK", Edit: "yangi matn", URL: "https://x.example/a.ics", URLText: "Taqvim"}
}

func (h *recHandler) ReplyTo(_ context.Context, from int64, data, text string) (Reply, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.replies = append(h.replies, fmt.Sprintf("%d|%s|%s", from, data, text))
	return Reply{Text: "Yuborildi"}, "eski karta"
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

func TestNetworkErrorHidesToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close() // ulanish rad etiladi
	b := New("123:SECRET-TOKEN", "", slog.Default())
	b.BaseURL = addr
	err := b.Notify(context.Background(), 1, "x", false)
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") || strings.Contains(err.Error(), "/bot") {
		t.Fatalf("token yoki URL sizib chiqdi: %v", err)
	}
	if !strings.Contains(err.Error(), "tarmoq xatosi: ") || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("aniq sabab ko'rinishi kerak: %v", err)
	}
}
