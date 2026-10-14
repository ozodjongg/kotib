package miniapp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

func TestHookFlow(t *testing.T) {
	st := store.NewMemory()
	ctx := context.Background()
	now := time.Now()
	_ = st.UpsertAccount(ctx, store.Account{OwnerID: 777, Status: store.StatusActive, ConsentAt: &now})
	_ = st.SaveSettings(ctx, 777, config.Default())
	s := &Server{BotToken: tok, Store: st, Ctl: &fakeCtl{}, PublicURL: "https://kotib.example"}
	srv := newTestServer(t, s)
	auth := "tma " + signInitData(tok, time.Now(), userJSON)

	req, _ := http.NewRequest("POST", srv.URL+"/api/hook", nil)
	req.Header.Set("Authorization", auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if !strings.HasPrefix(out.URL, "https://kotib.example/hook/") {
		t.Fatalf("url: %q", out.URL)
	}
	hook := srv.URL + strings.TrimPrefix(out.URL, "https://kotib.example")

	if code := doReq(t, hook+"?mode=auto&away=Hozir+rulda", "GET", ""); code != 200 {
		t.Fatalf("hook: %d", code)
	}
	got, _ := st.GetSettings(ctx, 777)
	if got.Mode != config.ModeAuto || got.Away != "Hozir rulda" {
		t.Fatalf("sozlama: %s %q", got.Mode, got.Away)
	}
	if code := doReq(t, hook+"?mode=draft&away=", "POST", ""); code != 200 {
		t.Fatalf("hook post: %d", code)
	}
	if got, _ = st.GetSettings(ctx, 777); got.Mode != config.ModeDraft || got.Away != "" {
		t.Fatalf("qaytish: %s %q", got.Mode, got.Away)
	}
	if code := doReq(t, hook+"?mode=xyz", "GET", ""); code != 400 {
		t.Fatal("noto'g'ri rejim rad etilishi kerak")
	}
	if code := doReq(t, hook+"?action=pause", "GET", ""); code != 200 {
		t.Fatal("pause")
	}
	if acc, _ := st.GetAccount(ctx, 777); acc.Status != store.StatusPaused {
		t.Fatalf("holat: %s", acc.Status)
	}
	if code := doReq(t, srv.URL+"/hook/"+strings.Repeat("x", 32), "GET", ""); code != 404 {
		t.Fatal("begona token")
	}
	// Yangi havola eskisini bekor qiladi.
	req, _ = http.NewRequest("POST", srv.URL+"/api/hook", nil)
	req.Header.Set("Authorization", auth)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if code := doReq(t, hook+"?mode=auto", "GET", ""); code != 404 {
		t.Fatal("eski havola ishlamasligi kerak")
	}
	if code := doReq(t, srv.URL+"/api/hook", "DELETE", auth); code != 200 {
		t.Fatal("delete")
	}
	if ok, _ := st.HasHook(ctx, 777); ok {
		t.Fatal("o'chirilishi kerak")
	}
}
