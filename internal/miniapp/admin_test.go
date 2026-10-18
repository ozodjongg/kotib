package miniapp

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

type notifyLog struct {
	mu   sync.Mutex
	sent map[int64][]string
}

func (n *notifyLog) fn(_ context.Context, owner int64, text string, _ bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent[owner] = append(n.sent[owner], text)
}

func (n *notifyLog) count(owner int64) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.sent[owner])
}

// Admin — 1; oddiy foydalanuvchilar — 20 (xavfsiz), 21 (avtomatik + notanishlar).
func setupAdmin(t *testing.T) (srvURL string, st *store.Memory, bill *billing.Service, ctl *fakeCtl, nl *notifyLog) {
	t.Helper()
	st = store.NewMemory()
	bill = &billing.Service{Store: st, API: &fakeStarsAPI{}, Price: 100, ProPrice: 200, TrialDays: 7, Exempt: map[int64]bool{1: true}}
	ctl = &fakeCtl{}
	nl = &notifyLog{sent: map[int64][]string{}}
	srv := newTestServer(t, &Server{BotToken: tok, Store: st, Ctl: ctl, Billing: bill, Admins: map[int64]bool{1: true}, Notify: nl.fn})
	ctx := context.Background()
	now := time.Now()
	for _, id := range []int64{1, 20, 21} {
		_ = st.UpsertAccount(ctx, store.Account{OwnerID: id, Status: store.StatusActive, ConsentAt: &now})
	}
	risky := config.Default()
	risky.Mode, risky.ReplyUnknown = config.ModeAuto, true
	_ = st.SaveSettings(ctx, 21, risky)
	st.Audit(ctx, 21, "flood_wait", "")
	st.Audit(ctx, 20, "draft_created", "chat=5")
	return srv.URL, st, bill, ctl, nl
}

func TestAdminOnlyForAdmins(t *testing.T) {
	url, _, _, _, _ := setupAdmin(t)
	for _, p := range []string{"/api/admin/overview", "/api/admin/users", "/api/admin/users/20", "/api/admin/payments"} {
		if r, _ := do(t, "GET", url+p, userAuth("20"), ""); r.StatusCode != 404 {
			t.Errorf("%s: oddiy foydalanuvchi %d oldi", p, r.StatusCode)
		}
		if r, _ := do(t, "GET", url+p, userAuth("1"), ""); r.StatusCode != 200 {
			t.Errorf("%s: admin %d oldi", p, r.StatusCode)
		}
	}
	if r, _ := do(t, "POST", url+"/api/admin/users/21/ban", userAuth("20"), `{"reason":"x"}`); r.StatusCode != 404 {
		t.Fatalf("oddiy foydalanuvchi bloklay oldi: %d", r.StatusCode)
	}
	if _, m := do(t, "GET", url+"/api/me", userAuth("1"), ""); m["admin"] != true {
		t.Fatalf("me.admin: %v", m["admin"])
	}
	if _, m := do(t, "GET", url+"/api/me", userAuth("20"), ""); m["admin"] != false {
		t.Fatalf("me.admin oddiy: %v", m["admin"])
	}
}

func TestAdminOverviewAndUsers(t *testing.T) {
	url, st, _, _, _ := setupAdmin(t)
	// /api/me ismni yozadi — ro'yxatda qidirish uchun.
	do(t, "GET", url+"/api/me", userAuth("20"), "")
	if acc, _ := st.GetAccount(context.Background(), 20); acc.Name != "X" || acc.SeenAt == nil {
		t.Fatalf("touch: %+v", acc)
	}
	_, m := do(t, "GET", url+"/api/admin/overview", userAuth("1"), "")
	if m["users"] != float64(3) || m["modes"].(map[string]any)["auto"] != float64(1) || m["signals"].(map[string]any)["suspicious"] != float64(1) {
		t.Fatalf("overview: %v", m)
	}
	_, m = do(t, "GET", url+"/api/admin/users?filter=risky", userAuth("1"), "")
	us := m["users"].([]any)
	if len(us) != 1 || us[0].(map[string]any)["id"] != float64(21) || us[0].(map[string]any)["safety"] != "high" {
		t.Fatalf("risky: %v", m)
	}
	_, m = do(t, "GET", url+"/api/admin/users?q=20", userAuth("1"), "")
	if m["total"] != float64(1) {
		t.Fatalf("qidiruv: %v", m)
	}
	_, m = do(t, "GET", url+"/api/admin/users/21", userAuth("1"), "")
	if m["user"].(map[string]any)["signal"] != "suspicious" || m["counts"].(map[string]any)["flood_wait"] != float64(1) {
		t.Fatalf("detail: %v", m)
	}
	if r, _ := do(t, "GET", url+"/api/admin/users/999", userAuth("1"), ""); r.StatusCode != 404 {
		t.Fatalf("yo'q foydalanuvchi: %d", r.StatusCode)
	}
}

func TestAdminGrantBanAndMessage(t *testing.T) {
	url, st, bill, ctl, nl := setupAdmin(t)
	ctx := context.Background()
	a := userAuth("1")
	if r, m := do(t, "POST", url+"/api/admin/users/20/grant", a, `{"days":30,"tier":"pro","notify":true}`); r.StatusCode != 200 {
		t.Fatalf("grant: %d %v", r.StatusCode, m)
	}
	if bs, _ := bill.Status(ctx, 20); !bs.Bonus || bs.Tier != billing.TierPro || nl.count(20) != 1 {
		t.Fatalf("grant natijasi: %+v", bs)
	}
	if r, _ := do(t, "POST", url+"/api/admin/users/20/grant", a, `{"days":99999,"tier":"pro"}`); r.StatusCode != 400 {
		t.Fatalf("katta muddat: %d", r.StatusCode)
	}
	if r, _ := do(t, "POST", url+"/api/admin/users/20/revoke-grant", a, ""); r.StatusCode != 200 {
		t.Fatalf("revoke: %d", r.StatusCode)
	}
	// Ban: kotib to'xtaydi, API yopiladi, xizmat yo'q; adminni bloklab bo'lmaydi.
	if r, _ := do(t, "POST", url+"/api/admin/users/1/ban", a, `{"reason":"x"}`); r.StatusCode != 400 {
		t.Fatalf("admin bloklandi: %d", r.StatusCode)
	}
	if r, _ := do(t, "POST", url+"/api/admin/users/21/ban", a, `{"reason":"spam","notify":true}`); r.StatusCode != 200 {
		t.Fatalf("ban: %d", r.StatusCode)
	}
	if ctl.paused.Load() != 1 || nl.count(21) != 1 || bill.Entitled(ctx, 21) {
		t.Fatalf("ban effekti: paused=%d notify=%d", ctl.paused.Load(), nl.count(21))
	}
	if acc, _ := st.GetAccount(ctx, 21); acc.Status != "paused" {
		t.Fatalf("holat: %s", acc.Status)
	}
	if r, _ := do(t, "GET", url+"/api/settings", userAuth("21"), ""); r.StatusCode != 403 {
		t.Fatalf("bloklangan sozlamalarni oldi: %d", r.StatusCode)
	}
	if r, m := do(t, "GET", url+"/api/me", userAuth("21"), ""); r.StatusCode != 200 || m["banned"].(map[string]any)["reason"] != "spam" {
		t.Fatalf("me banned: %d %v", r.StatusCode, m)
	}
	if _, m := do(t, "GET", url+"/api/admin/users?filter=banned", a, ""); m["total"] != float64(1) {
		t.Fatalf("banned filtr: %v", m)
	}
	if r, _ := do(t, "DELETE", url+"/api/admin/users/21/ban", a, ""); r.StatusCode != 200 {
		t.Fatalf("unban: %d", r.StatusCode)
	}
	if r, _ := do(t, "GET", url+"/api/settings", userAuth("21"), ""); r.StatusCode != 200 {
		t.Fatalf("blokdan keyin: %d", r.StatusCode)
	}
	// Xabar va ommaviy xabar.
	if r, _ := do(t, "POST", url+"/api/admin/users/20/message", a, `{"text":"Salom"}`); r.StatusCode != 200 || nl.count(20) != 2 {
		t.Fatalf("message: %d %d", r.StatusCode, nl.count(20))
	}
	if r, _ := do(t, "POST", url+"/api/admin/users/20/message", a, `{"text":"  "}`); r.StatusCode != 400 {
		t.Fatalf("bo'sh xabar: %d", r.StatusCode)
	}
	_ = st.SetBan(ctx, store.Ban{OwnerID: 21, By: 1})
	_, m := do(t, "POST", url+"/api/admin/broadcast", a, `{"text":"Yangilik"}`)
	if m["recipients"] != float64(2) { // 1 va 20; 21 bloklangan
		t.Fatalf("broadcast: %v", m)
	}
	deadline := time.Now().Add(2 * time.Second)
	for nl.count(1) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if nl.count(1) != 1 || nl.count(21) != 2 {
		t.Fatalf("broadcast yetkazilishi: admin=%d banned=%d", nl.count(1), nl.count(21))
	}
}

func TestAssess(t *testing.T) {
	now := time.Now()
	st := config.Default()
	if a := assess(st, map[string]int{}, now); a.Safety != safetySafe || a.Signal != signalIdle || a.Mode != "draft" {
		t.Fatalf("standart: %+v", a)
	}
	if a := assess(st, map[string]int{"draft_created": 4, "draft_sent": 3, "draft_rejected": 1}, now); a.Signal != signalOK || a.Approval != 75 || a.Sent != 3 {
		t.Fatalf("foydali: %+v", a)
	}
	auto := config.ModeAuto
	st.Rules = []config.Rule{{ID: "r", Mode: &auto}}
	if a := assess(st, map[string]int{"auto_sent": 350}, now); a.Safety != safetyMedium || a.Signal != signalWatch {
		t.Fatalf("avto qoida: %+v", a)
	}
	st.AllowLinks = true
	if a := assess(st, map[string]int{"output_rejected": 12}, now); a.Safety != safetyHigh || a.Signal != signalSuspicious {
		t.Fatalf("xavfli: %+v", a)
	}
	until := now.Add(time.Hour)
	st = config.Default()
	st.Mode, st.ShadowUntil = config.ModeAuto, &until
	if a := assess(st, nil, now); a.Mode != "shadow" || a.Safety != safetySafe {
		t.Fatalf("soya: %+v", a)
	}
}
