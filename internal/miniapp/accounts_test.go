package miniapp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// doAs — so'rov tanlangan akkaunt sarlavhasi bilan.
func doAs(t *testing.T, method, url, auth, account, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Authorization", auth)
	if account != "" {
		req.Header.Set(AccountHeader, account)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp, m
}

func TestLinkedAccountsAPI(t *testing.T) {
	st := store.NewMemory()
	bill := &billing.Service{Store: st, API: &fakeStarsAPI{}, Price: 150, ProPrice: 250, TrialDays: 7}
	srv := newTestServer(t, &Server{BotToken: tok, Store: st, Ctl: &fakeCtl{}, Billing: bill, BotUsername: "kotib_bot"})
	ctx := context.Background()
	a := userAuth("60")
	do(t, "POST", srv.URL+"/api/consent", a, `{"accepted":true}`)

	// Sinovda (Pro funksiyalari ochiq) havola olinadi.
	r, m := do(t, "POST", srv.URL+"/api/accounts/link", a, `{"label":" Ish  akkaunt "}`)
	link, _ := m["link"].(string)
	if r.StatusCode != 200 || !strings.HasPrefix(link, "https://t.me/kotib_bot?start=link_") {
		t.Fatalf("link: %d %v", r.StatusCode, m)
	}
	token := strings.TrimPrefix(link, "https://t.me/kotib_bot?start=link_")
	if lt, err := st.GetLinkToken(ctx, store.TokenHash(token)); err != nil || lt.ParentID != 60 || lt.Label != "Ish akkaunt" {
		t.Fatalf("token: %+v %v", lt, err)
	}

	// Bot orqali rozilik (botcmd) — shu yerda to'g'ridan-to'g'ri.
	now := time.Now()
	_, _ = st.AddLink(ctx, store.Link{ChildID: 61, ParentID: 60, Label: "Ish"})
	_ = st.UpsertAccount(ctx, store.Account{OwnerID: 61, Status: store.StatusPending, ConsentAt: &now})

	_, me := doAs(t, "GET", srv.URL+"/api/me", a, "61", "")
	if me["account_id"] != float64(61) || me["main_id"] != float64(60) || me["status"] != "pending" {
		t.Fatalf("me (qo'shimcha): %v", me)
	}
	if accs, _ := me["accounts"].([]any); len(accs) != 2 {
		t.Fatalf("ro'yxat: %v", me["accounts"])
	}
	// Sozlama tanlangan akkauntga yoziladi.
	_, cur := doAs(t, "GET", srv.URL+"/api/settings", a, "61", "")
	cur["owner_name"] = "Ish"
	body, _ := json.Marshal(cur)
	if r, m := doAs(t, "PUT", srv.URL+"/api/settings", a, "61", string(body)); r.StatusCode != 200 {
		t.Fatalf("settings: %d %v", r.StatusCode, m)
	}
	if s61, _ := st.GetSettings(ctx, 61); s61.OwnerName != "Ish" {
		t.Fatal("qo'shimcha akkaunt sozlamasi")
	}
	if s60, _ := st.GetSettings(ctx, 60); s60.OwnerName == "Ish" {
		t.Fatal("asosiy akkaunt sozlamasi o'zgarmasligi kerak")
	}
	// Begona akkaunt sarlavhasi va qo'shimcha akkauntning o'zi — 403.
	if r, _ := doAs(t, "GET", srv.URL+"/api/me", a, "99", ""); r.StatusCode != 403 {
		t.Fatalf("begona: %d", r.StatusCode)
	}
	if r, _ := do(t, "GET", srv.URL+"/api/me", userAuth("61"), ""); r.StatusCode != 403 {
		t.Fatalf("qo'shimcha o'zi: %d", r.StatusCode)
	}
	// Obuna har doim asosiy akkaunt bo'yicha (sarlavha e'tiborsiz).
	if r, _ := doAs(t, "GET", srv.URL+"/api/billing", a, "61", ""); r.StatusCode != 200 {
		t.Fatalf("billing: %d", r.StatusCode)
	}
	_, accs := do(t, "GET", srv.URL+"/api/accounts", a, "")
	if accs["can_add"] != true || accs["max"] != float64(billing.MaxLinkedAccounts) {
		t.Fatalf("accounts: %v", accs)
	}

	// Qo'shimcha akkaunt tanlanganda "o'chirish" — faqat o'sha akkaunt.
	if r, _ := doAs(t, "DELETE", srv.URL+"/api/account", a, "61", `{"confirm":"DELETE"}`); r.StatusCode != 200 {
		t.Fatalf("unlink: %d", r.StatusCode)
	}
	if _, err := st.GetLink(ctx, 61); err == nil {
		t.Fatal("bog'lanish o'chirilishi kerak")
	}
	if _, err := st.GetAccount(ctx, 61); err == nil {
		t.Fatal("qo'shimcha akkaunt o'chirilishi kerak")
	}
	if _, err := st.GetAccount(ctx, 60); err != nil {
		t.Fatal("asosiy akkaunt saqlanishi kerak")
	}
	if r, _ := do(t, "DELETE", srv.URL+"/api/accounts/61", a, ""); r.StatusCode != 404 {
		t.Fatalf("yo'q akkaunt: %d", r.StatusCode)
	}

	// Advanced tarif — qo'shimcha akkaunt yo'q (402).
	_ = bill.PaymentReceived(ctx, 60, billing.PaymentInfo{Currency: "XTR", Amount: 150, Payload: "sub:60:advanced:0123456789abcdef",
		ChargeID: "a1", ExpiresAt: time.Now().Add(30 * 24 * time.Hour)})
	if r, _ := do(t, "POST", srv.URL+"/api/accounts/link", a, `{"label":"x"}`); r.StatusCode != 402 {
		t.Fatalf("advanced: %d", r.StatusCode)
	}
}

func TestDeleteMainUnlinksChildren(t *testing.T) {
	st := store.NewMemory()
	srv := newTestServer(t, &Server{BotToken: tok, Store: st, Ctl: &fakeCtl{}})
	ctx := context.Background()
	a := userAuth("70")
	do(t, "POST", srv.URL+"/api/consent", a, `{"accepted":true}`)
	now := time.Now()
	_, _ = st.AddLink(ctx, store.Link{ChildID: 71, ParentID: 70})
	_ = st.UpsertAccount(ctx, store.Account{OwnerID: 71, Status: store.StatusActive, ConsentAt: &now})
	if r, _ := do(t, "DELETE", srv.URL+"/api/account", a, `{"confirm":"DELETE"}`); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	if _, err := st.GetAccount(ctx, 71); err == nil {
		t.Fatal("asosiy o'chirilsa qo'shimchalar ham o'chirilishi kerak")
	}
	if _, err := st.GetLink(ctx, 71); err == nil {
		t.Fatal("bog'lanish qolmasligi kerak")
	}
}

func TestStatsEndpoint(t *testing.T) {
	st := store.NewMemory()
	bill := &billing.Service{Store: st, API: &fakeStarsAPI{}, Price: 150, ProPrice: 250, TrialDays: 7}
	srv := newTestServer(t, &Server{BotToken: tok, Store: st, Ctl: &fakeCtl{}, Billing: bill})
	ctx := context.Background()
	a := userAuth("80")
	do(t, "POST", srv.URL+"/api/consent", a, `{"accepted":true}`)
	st.Audit(ctx, 80, "draft_created", "id=1 chat=5")
	st.Audit(ctx, 80, "draft_sent", "1")
	st.Audit(ctx, 80, "auto_sent", "chat=5")

	r, m := do(t, "GET", srv.URL+"/api/stats", a, "")
	if r.StatusCode != 200 || m["prepared"] != float64(2) || m["sent"] != float64(2) || m["approval"] != float64(100) {
		t.Fatalf("stats: %d %v", r.StatusCode, m)
	}
	if series, _ := m["series"].([]any); len(series) != 7 {
		t.Fatalf("series: %v", m["series"])
	}
	if top, _ := m["top_chats"].([]any); len(top) != 1 || top[0].(map[string]any)["name"] != "Kontakt" {
		t.Fatalf("top: %v", m["top_chats"])
	}
	if _, m := do(t, "GET", srv.URL+"/api/stats?days=30", a, ""); m["days"] != float64(30) {
		t.Fatalf("30: %v", m)
	}
	if r, _ := do(t, "GET", srv.URL+"/api/stats?days=9", a, ""); r.StatusCode != 400 {
		t.Fatal(r.StatusCode)
	}
	_ = bill.PaymentReceived(ctx, 80, billing.PaymentInfo{Currency: "XTR", Amount: 150, Payload: "sub:80:advanced:0123456789abcdef",
		ChargeID: "a1", ExpiresAt: time.Now().Add(30 * 24 * time.Hour)})
	if r, _ := do(t, "GET", srv.URL+"/api/stats", a, ""); r.StatusCode != 402 {
		t.Fatalf("advanced: %d", r.StatusCode)
	}
}
