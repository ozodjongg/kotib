package miniapp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

func consented(t *testing.T, st *store.Memory, id int64) {
	t.Helper()
	now := time.Now()
	_ = st.UpsertAccount(context.Background(), store.Account{OwnerID: id, Status: store.StatusActive, ConsentAt: &now})
}

func TestStyleLearnEndpoint(t *testing.T) {
	srv, st, ctl := setup(t)
	a := userAuth("1")
	if r, _ := do(t, "POST", srv.URL+"/api/style/learn", a, ""); r.StatusCode != 403 {
		t.Fatalf("rozilikdan oldin: %d", r.StatusCode)
	}
	consented(t, st, 1)
	if r, _ := do(t, "POST", srv.URL+"/api/style/learn", a, ""); r.StatusCode != 202 {
		t.Fatalf("boshlash: %d", r.StatusCode)
	}
	ctl.learnErr = errors.New("uslub o'rganilmoqda, biroz kuting")
	if r, m := do(t, "POST", srv.URL+"/api/style/learn", a, ""); r.StatusCode != 409 || m["error"] != "uslub o'rganilmoqda, biroz kuting" {
		t.Fatalf("band: %d %v", r.StatusCode, m)
	}
}

func TestStyleShadowEndpointAndServerFields(t *testing.T) {
	srv, st, _ := setup(t)
	ctx := context.Background()
	a := userAuth("1")
	consented(t, st, 1)
	if r, _ := do(t, "POST", srv.URL+"/api/style/shadow", a, `{"days":30}`); r.StatusCode != 400 {
		t.Fatalf("30 kun: %d", r.StatusCode)
	}
	if r, _ := do(t, "POST", srv.URL+"/api/style/shadow", a, `{"days":7}`); r.StatusCode != 200 {
		t.Fatalf("yoqish: %d", r.StatusCode)
	}
	s, _ := st.GetSettings(ctx, 1)
	if s.ShadowUntil == nil || !s.Learn || s.ShadowUntil.Sub(time.Now()) < 6*24*time.Hour {
		t.Fatalf("soya: %+v", s)
	}
	// Server maydonlari: Mini App PUT ularni o'zgartira olmaydi.
	s.StyleScore = 77
	_ = st.SaveSettings(ctx, 1, s)
	_, cur := do(t, "GET", srv.URL+"/api/settings", a, "")
	cur["style_score"] = 5
	delete(cur, "shadow_until")
	cur["style_profile"] = "Qisqa yozaman"
	cur["contact_notes"] = map[string]string{"100": "onam"}
	raw, _ := json.Marshal(cur)
	if r, m := do(t, "PUT", srv.URL+"/api/settings", a, string(raw)); r.StatusCode != 200 {
		t.Fatalf("saqlash: %d %v", r.StatusCode, m)
	}
	s, _ = st.GetSettings(ctx, 1)
	if s.StyleScore != 77 || s.ShadowUntil == nil || s.StyleProfile != "Qisqa yozaman" || s.ContactNotes[100] != "onam" {
		t.Fatalf("server maydonlari saqlanishi kerak: %+v", s)
	}
	if r, _ := do(t, "POST", srv.URL+"/api/style/shadow", a, `{"days":0}`); r.StatusCode != 200 {
		t.Fatal("o'chirish")
	}
	if s, _ := st.GetSettings(ctx, 1); s.ShadowUntil != nil {
		t.Fatal("soya o'chirilishi kerak")
	}
}
