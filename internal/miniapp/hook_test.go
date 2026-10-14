package miniapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/ics"
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
	// for=<daqiqa> — holat o'zi tugaydi.
	if code := doReq(t, hook+"?away=Dars+qilyapman&for=90", "GET", ""); code != 200 {
		t.Fatalf("hook for: %d", code)
	}
	got, _ = st.GetSettings(ctx, 777)
	if got.Away != "Dars qilyapman" || got.AwayUntil == nil || got.AwayUntil.Sub(time.Now()) < 89*time.Minute ||
		got.CurrentAway(time.Now().Add(91*time.Minute)) != "" {
		t.Fatalf("muddatli holat: %q %v", got.Away, got.AwayUntil)
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

func TestICSEndpoint(t *testing.T) {
	st := store.NewMemory()
	ctx := context.Background()
	_ = st.UpsertAccount(ctx, store.Account{OwnerID: 7, Status: store.StatusActive})
	signer := &ics.Signer{Key: []byte("k"), Base: "https://kotib.example"}
	s := &Server{BotToken: tok, Store: st, PublicURL: "https://kotib.example", ICS: signer}
	srv := newTestServer(t, s)
	id, _ := st.CreateAction(ctx, store.Action{OwnerID: 7, Kind: store.ActionEvent,
		Payload: []byte(`{"title":"Aziz bilan uchrashuv","start":"2026-10-15T15:00:00+05:00","tz":"Asia/Tashkent","duration_min":30,"with":"Aziz"}`)})
	path := strings.TrimPrefix(signer.URL(7, id), "https://kotib.example")

	if code := doReq(t, srv.URL+path, "GET", ""); code != 404 {
		t.Fatal("tasdiqlanmagan amal berilmasligi kerak")
	}
	_ = st.SetActionStatus(ctx, 7, id, store.ActionPending, store.ActionDone)
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/calendar") ||
		!strings.Contains(string(b), "DTSTART:20261015T100000Z") || !strings.Contains(string(b), "DTEND:20261015T103000Z") {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	bad := strings.Replace(path, "/7/", "/8/", 1)
	if code := doReq(t, srv.URL+bad, "GET", ""); code != 404 {
		t.Fatal("imzo boshqa egaga mos kelmasligi kerak")
	}
}

func TestTGLoginPage(t *testing.T) {
	srv := newTestServer(t, &Server{BotToken: tok, Store: store.NewMemory()})
	resp, err := http.Get(srv.URL + "/tglogin?token=QUJDREVGR0hJSktM")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `href="tg://login?token=QUJDREVGR0hJSktM"`) {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	for _, bad := range []string{"", "abc", "QUJD%22%3E%3Cscript%3E", "javascript:alert(1)aaaaaa"} {
		if code := doReq(t, srv.URL+"/tglogin?token="+bad, "GET", ""); code != 400 {
			t.Fatalf("%q rad etilishi kerak: %d", bad, code)
		}
	}
}

func TestQuickAway(t *testing.T) {
	st := store.NewMemory()
	ctx := context.Background()
	srv := newTestServer(t, &Server{BotToken: tok, Store: st, Ctl: &fakeCtl{}})
	a := userAuth("41")
	if r, _ := do(t, "POST", srv.URL+"/api/away", a, `{"away":"Sayrdaman","minutes":60}`); r.StatusCode != 403 {
		t.Fatalf("rozilikdan oldin: %d", r.StatusCode)
	}
	do(t, "POST", srv.URL+"/api/consent", a, `{"accepted":true}`)
	s0 := config.Default()
	s0.OwnerName = "Aziz"
	_ = st.SaveSettings(ctx, 41, s0)

	r, m := do(t, "POST", srv.URL+"/api/away", a, `{"away":"  Sayr   qilyapman ","minutes":60}`)
	if r.StatusCode != 200 || m["away"] != "Sayr qilyapman" || m["away_until"] == nil {
		t.Fatalf("away: %d %v", r.StatusCode, m)
	}
	got, _ := st.GetSettings(ctx, 41)
	if got.OwnerName != "Aziz" || got.CurrentAway(time.Now()) != "Sayr qilyapman" {
		t.Fatalf("boshqa sozlamalar saqlanishi kerak: %+v", got)
	}
	// Muddatsiz va tozalash.
	do(t, "POST", srv.URL+"/api/away", a, `{"away":"Safardaman","minutes":0}`)
	if got, _ = st.GetSettings(ctx, 41); got.AwayUntil != nil || got.Away != "Safardaman" {
		t.Fatalf("muddatsiz: %+v", got)
	}
	do(t, "POST", srv.URL+"/api/away", a, `{"away":"","minutes":30}`)
	if got, _ = st.GetSettings(ctx, 41); got.Away != "" || got.AwayUntil != nil {
		t.Fatalf("tozalash: %+v", got)
	}
	for _, bad := range []string{`{"away":"x","minutes":-1}`, `{"away":"x","minutes":5000}`, `{"away":"` + strings.Repeat("a", 101) + `"}`} {
		if r, _ := do(t, "POST", srv.URL+"/api/away", a, bad); r.StatusCode != 400 {
			t.Fatalf("%s: %d", bad, r.StatusCode)
		}
	}
}
