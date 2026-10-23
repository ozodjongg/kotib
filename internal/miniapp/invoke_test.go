package miniapp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

func TestInvoke(t *testing.T) {
	st := store.NewMemory()
	s := &Server{BotToken: tok, Store: st, Ctl: &fakeCtl{}}
	ctx := context.Background()

	// Sozlamalar bot chatidan ham brauzerdagidek o'qiladi va saqlanadi (Validate bilan).
	set := config.Default()
	set.Mode = config.ModeAuto
	if code, out := s.Invoke(ctx, 7, 0, "PUT", "/api/settings", set); code != 200 {
		t.Fatalf("put: %d %s", code, out)
	}
	code, out := s.Invoke(ctx, 7, 0, "GET", "/api/settings", nil)
	var got config.Settings
	if code != 200 || json.Unmarshal(out, &got) != nil || got.Mode != config.ModeAuto {
		t.Fatalf("get: %d %s", code, out)
	}
	set.Mode = "yomon"
	if code, _ := s.Invoke(ctx, 7, 0, "PUT", "/api/settings", set); code != 400 {
		t.Fatalf("validatsiya: %d", code)
	}

	// Kod/parol, rozilik va akkauntni o'chirish — faqat ilovada.
	for _, c := range []struct{ m, p string }{{"POST", "/api/login/code"}, {"DELETE", "/api/account"}, {"POST", "/api/consent"}, {"POST", "/api/hook"}} {
		if code, _ := s.Invoke(ctx, 7, 0, c.m, c.p, map[string]string{"confirm": "DELETE"}); code != 403 {
			t.Errorf("%s %s: %d", c.m, c.p, code)
		}
	}
	if _, err := st.GetAccount(ctx, 7); err != nil {
		t.Fatal("akkaunt o'chmasligi kerak")
	}

	// Begona akkaunt tanlab bo'lmaydi.
	if code, _ := s.Invoke(ctx, 7, 99, "GET", "/api/settings", nil); code != 403 {
		t.Fatalf("begona akkaunt: %d", code)
	}
	// Bloklangan foydalanuvchi.
	_ = st.SetBan(ctx, store.Ban{OwnerID: 7, Reason: "spam"})
	if code, _ := s.Invoke(ctx, 7, 0, "GET", "/api/settings", nil); code != 403 {
		t.Fatalf("ban: %d", code)
	}
	if code, _ := s.Invoke(ctx, 0, 0, "GET", "/api/settings", nil); code != 401 {
		t.Fatalf("foydalanuvchisiz: %d", code)
	}
}
