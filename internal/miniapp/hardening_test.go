package miniapp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

func TestFailedSendKeepsDraftPending(t *testing.T) {
	srv, st, ctl := setup(t)
	ctl.sendErr = errors.New("tarmoq")
	ctx := context.Background()
	id, _ := st.CreateDraft(ctx, store.Draft{OwnerID: 1, ChatID: 9, Reply: "salom"})
	if r, _ := do(t, "POST", srv.URL+"/api/drafts/"+itoa(id)+"/send", userAuth("1"), ""); r.StatusCode != 502 {
		t.Fatalf("yuborish xatosi 502 bo'lishi kerak: %d", r.StatusCode)
	}
	d, err := st.GetDraft(ctx, 1, id)
	if err != nil || d.Status != store.DraftPending {
		t.Fatalf("yuborilmagan draft yo'qolmasligi kerak: %+v %v", d, err)
	}
	ctl.sendErr = nil
	if r, _ := do(t, "POST", srv.URL+"/api/drafts/"+itoa(id)+"/send", userAuth("1"), ""); r.StatusCode != 200 {
		t.Fatalf("qayta urinish: %d", r.StatusCode)
	}
}

func TestSendDraftRejectsUnknownFields(t *testing.T) {
	srv, st, _ := setup(t)
	id, _ := st.CreateDraft(context.Background(), store.Draft{OwnerID: 1, ChatID: 9, Reply: "salom"})
	if r, _ := do(t, "POST", srv.URL+"/api/drafts/"+itoa(id)+"/send", userAuth("1"), `{"chat_id":777}`); r.StatusCode != 400 {
		t.Fatalf("noma'lum maydon rad etilishi kerak: %d", r.StatusCode)
	}
}

func TestLoginTooManyAttempts(t *testing.T) {
	srv, _, ctl := setup(t)
	a := userAuth("11")
	do(t, "POST", srv.URL+"/api/consent", a, `{"accepted":true}`)
	ctl.loginErr = ErrTooManyAttempts
	if r, _ := do(t, "POST", srv.URL+"/api/login/phone", a, `{"phone":"+998901234567"}`); r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("429 kutilgan: %d", r.StatusCode)
	}
}

func TestCodeRequiresConsent(t *testing.T) {
	srv, _, _ := setup(t)
	if r, _ := do(t, "POST", srv.URL+"/api/login/code", userAuth("12"), `{"code":"12345"}`); r.StatusCode != 403 {
		t.Fatalf("rozilikdan oldin kod: %d", r.StatusCode)
	}
}

func TestSettingsRulesNeverNull(t *testing.T) {
	srv, _, _ := setup(t)
	_, m := do(t, "GET", srv.URL+"/api/settings", userAuth("13"), "")
	if _, ok := m["rules"].([]any); !ok {
		t.Fatalf("rules massiv bo'lishi kerak: %#v", m["rules"])
	}
}

func TestSecurityHeadersAndStatic(t *testing.T) {
	dir := t.TempDir()
	st := store.NewMemory()
	srv := newTestServer(t, &Server{BotToken: tok, Store: st, Ctl: &fakeCtl{}, StaticDir: dir})
	writeFile(t, dir, "index.html", "<p>hi</p>")
	writeFile(t, dir, ".env", "SECRET=1")
	writeFile(t, dir, "sub/x.txt", "x")

	r, _ := do(t, "GET", srv.URL+"/", "", "")
	if r.StatusCode != 200 {
		t.Fatalf("index: %d", r.StatusCode)
	}
	csp := r.Header.Get("Content-Security-Policy")
	if csp == "" || containsStr(csp, "unsafe-inline") {
		t.Fatalf("CSP: %q", csp)
	}
	for _, p := range []string{"/.env", "/sub/", "/api/nope"} {
		if r, _ := do(t, "GET", srv.URL+p, "", ""); r.StatusCode != 404 {
			t.Errorf("%s: %d (404 kutilgan)", p, r.StatusCode)
		}
	}
}

func TestLegalPages(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, &Server{BotToken: tok, Store: store.NewMemory(), Ctl: &fakeCtl{}, StaticDir: dir})
	writeFile(t, dir, "privacy.html", "<h1>Maxfiylik</h1>")
	writeFile(t, dir, "terms.html", "<h1>Shartlar</h1>")
	for p, want := range map[string]string{"/privacy": "Maxfiylik", "/terms": "Shartlar", "/privacy.html": "Maxfiylik"} {
		r, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || !strings.Contains(string(body), want) || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
			t.Errorf("%s: %d %q %q", p, r.StatusCode, r.Header.Get("Content-Type"), body)
		}
	}
}
