package miniapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

type fakeCtl struct {
	sent     atomic.Int32
	revoked  atomic.Int32
	sendErr  error
	loginErr error
}

func (f *fakeCtl) BeginLogin(context.Context, int64, string) error { return f.loginErr }
func (f *fakeCtl) BeginQRLogin(context.Context, int64) error       { return f.loginErr }
func (f *fakeCtl) SubmitCode(int64, string) error                  { return nil }
func (f *fakeCtl) SubmitPassword(int64, string) error              { return nil }
func (f *fakeCtl) LoginState(int64) LoginState                     { return LoginState{Step: "idle"} }
func (f *fakeCtl) Pause(int64)                                     {}
func (f *fakeCtl) Resume(context.Context, int64) error             { return nil }
func (f *fakeCtl) Revoke(context.Context, int64) error             { f.revoked.Add(1); return nil }
func (f *fakeCtl) SendDraft(context.Context, int64, store.Draft) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent.Add(1)
	return nil
}
func (f *fakeCtl) Contacts(context.Context, int64) ([]ContactInfo, error) { return nil, nil }

func setup(t *testing.T) (*httptest.Server, *store.Memory, *fakeCtl) {
	t.Helper()
	st := store.NewMemory()
	ctl := &fakeCtl{}
	srv := httptest.NewServer((&Server{BotToken: tok, Store: st, Ctl: ctl}).Handler())
	t.Cleanup(srv.Close)
	return srv, st, ctl
}

func userAuth(id string) string {
	return "tma " + signInitData(tok, time.Now(), `{"id":`+id+`,"first_name":"X"}`)
}

func do(t *testing.T, method, url, auth, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
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

func TestAPIRequiresAuth(t *testing.T) {
	srv, _, _ := setup(t)
	for _, p := range []string{"/api/me", "/api/settings", "/api/drafts", "/api/contacts"} {
		if r, _ := do(t, "GET", srv.URL+p, "", ""); r.StatusCode != 401 {
			t.Errorf("%s: auth'siz %d", p, r.StatusCode)
		}
		if r, _ := do(t, "GET", srv.URL+p, "tma forged", ""); r.StatusCode != 401 {
			t.Errorf("%s: soxta initData %d", p, r.StatusCode)
		}
	}
}

func TestLoginRequiresConsent(t *testing.T) {
	srv, _, _ := setup(t)
	a := userAuth("10")
	if r, _ := do(t, "POST", srv.URL+"/api/login/phone", a, `{"phone":"+998901234567"}`); r.StatusCode != 403 {
		t.Fatalf("rozilikdan oldin login: %d", r.StatusCode)
	}
	if r, _ := do(t, "POST", srv.URL+"/api/consent", a, `{"accepted":true}`); r.StatusCode != 200 {
		t.Fatal("consent")
	}
	if r, _ := do(t, "POST", srv.URL+"/api/login/phone", a, `{"phone":"+998901234567"}`); r.StatusCode != 200 {
		t.Fatalf("rozilikdan keyin login: %d", r.StatusCode)
	}
	if r, _ := do(t, "POST", srv.URL+"/api/login/phone", a, `{"phone":"abc"}`); r.StatusCode != 400 {
		t.Fatalf("noto'g'ri telefon: %d", r.StatusCode)
	}
}

func TestSettingsValidatedAndIsolated(t *testing.T) {
	srv, _, _ := setup(t)
	a, b := userAuth("1"), userAuth("2")
	if r, _ := do(t, "PUT", srv.URL+"/api/settings", a, `{"mode":"hack"}`); r.StatusCode != 400 {
		t.Fatalf("noto'g'ri mode: %d", r.StatusCode)
	}
	_, def := do(t, "GET", srv.URL+"/api/settings", a, "")
	def["mode"] = "off"
	raw, _ := json.Marshal(def)
	if r, _ := do(t, "PUT", srv.URL+"/api/settings", a, string(raw)); r.StatusCode != 200 {
		t.Fatalf("saqlash: %d", r.StatusCode)
	}
	_, got := do(t, "GET", srv.URL+"/api/settings", b, "")
	if got["mode"] != "draft" {
		t.Fatalf("boshqa foydalanuvchi sozlamasi o'zgarmasligi kerak: %v", got["mode"])
	}
}

func TestDraftSendOnceAndIsolation(t *testing.T) {
	srv, st, ctl := setup(t)
	ctx := context.Background()
	id, _ := st.CreateDraft(ctx, store.Draft{OwnerID: 1, ChatID: 9, Reply: "salom"})
	path := srv.URL + "/api/drafts/" + itoa(id)

	if r, _ := do(t, "POST", path+"/send", userAuth("2"), ""); r.StatusCode != 404 {
		t.Fatalf("begona draftni yuborib bo'lmaydi: %d", r.StatusCode)
	}
	if r, _ := do(t, "POST", path+"/send", userAuth("1"), `{"reply":"tahrirlangan"}`); r.StatusCode != 200 {
		t.Fatalf("birinchi yuborish: %d", r.StatusCode)
	}
	if r, _ := do(t, "POST", path+"/send", userAuth("1"), ""); r.StatusCode == 200 {
		t.Fatal("ikkinchi marta yuborilmasligi kerak")
	}
	if ctl.sent.Load() != 1 {
		t.Fatalf("aniq 1 marta yuborilishi kerak: %d", ctl.sent.Load())
	}
}

func TestDeleteNeedsConfirm(t *testing.T) {
	srv, st, ctl := setup(t)
	a := userAuth("5")
	_ = st.UpsertAccount(context.Background(), store.Account{OwnerID: 5, Status: store.StatusActive})
	if r, _ := do(t, "DELETE", srv.URL+"/api/account", a, `{"confirm":"nope"}`); r.StatusCode != 400 {
		t.Fatalf("tasdiqsiz o'chirish: %d", r.StatusCode)
	}
	if _, err := st.GetAccount(context.Background(), 5); err != nil {
		t.Fatal("akkaunt o'chib ketgan")
	}
	if r, _ := do(t, "DELETE", srv.URL+"/api/account", a, `{"confirm":"DELETE"}`); r.StatusCode != 200 {
		t.Fatalf("o'chirish: %d", r.StatusCode)
	}
	if ctl.revoked.Load() != 1 {
		t.Fatal("Revoke chaqirilmadi")
	}
	if _, err := st.GetAccount(context.Background(), 5); err == nil {
		t.Fatal("akkaunt o'chirilishi kerak edi")
	}
}

func itoa(i int64) string { b, _ := json.Marshal(i); return string(b) }
