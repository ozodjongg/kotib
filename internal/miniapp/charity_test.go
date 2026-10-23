package miniapp

import (
	"context"
	"strconv"
	"testing"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

func TestCharityAPI(t *testing.T) {
	url, st, bill, _, _ := setupAdmin(t)
	ctx := context.Background()
	user, admin := userAuth("20"), userAuth("1")
	if _, m := do(t, "GET", url+"/api/charity", user, ""); m["enabled"] != false {
		t.Fatalf("o'chiq: %v", m)
	}
	if r, _ := do(t, "POST", url+"/api/charity/donate", user, `{"stars":50}`); r.StatusCode != 404 {
		t.Fatalf("o'chiq donate: %d", r.StatusCode)
	}
	bill.CharityPercent, bill.CharityURL = 10, "https://example.org"
	if _, m := do(t, "GET", url+"/api/charity", user, ""); m["enabled"] != true || m["percent"] != float64(10) || m["url"] != "https://example.org" {
		t.Fatalf("hisobot: %v", m)
	}
	if r, _ := do(t, "POST", url+"/api/charity/donate", user, `{"stars":0}`); r.StatusCode != 400 {
		t.Fatalf("0 ⭐: %d", r.StatusCode)
	}
	if r, m := do(t, "POST", url+"/api/charity/donate", user, `{"stars":100}`); r.StatusCode != 200 || m["link"] == "" {
		t.Fatalf("donate: %d %v", r.StatusCode, m)
	}

	id, _, _ := st.AddCharityPayout(ctx, store.CharityPayout{Kind: store.CharityMonthly, Period: "2026-09", Stars: 40})
	paid := url + "/api/admin/charity/" + strconv.FormatInt(id, 10) + "/paid"
	if r, _ := do(t, "POST", paid, user, `{"proof":"x"}`); r.StatusCode != 404 {
		t.Fatalf("oddiy foydalanuvchi: %d", r.StatusCode)
	}
	if r, _ := do(t, "POST", paid, admin, `{"proof":""}`); r.StatusCode != 400 {
		t.Fatalf("isbotsiz: %d", r.StatusCode)
	}
	if r, _ := do(t, "POST", paid, admin, `{"proof":"https://tonviewer.com/transaction/abc"}`); r.StatusCode != 200 {
		t.Fatalf("paid: %d", r.StatusCode)
	}
	if r, _ := do(t, "POST", url+"/api/admin/charity/manual", user, `{"stars":10}`); r.StatusCode != 404 {
		t.Fatalf("oddiy foydalanuvchi manual: %d", r.StatusCode)
	}
	if r, m := do(t, "POST", url+"/api/admin/charity/manual", admin, `{"stars":500,"note":"naqd","proof":""}`); r.StatusCode != 200 || m["id"] == nil {
		t.Fatalf("manual: %d %v", r.StatusCode, m)
	}
	_, m := do(t, "GET", url+"/api/charity", user, "")
	if m["paid_stars"] != float64(540) {
		t.Fatalf("o'tkazilgan: %v", m)
	}
	if ps, _ := m["payouts"].([]any); len(ps) != 2 {
		t.Fatalf("payouts: %v", m["payouts"])
	}
}

func TestCharityMine(t *testing.T) {
	url, st, bill, _, _ := setupAdmin(t)
	ctx := context.Background()
	bill.CharityPercent = 10
	_, _ = st.AddPayment(ctx, store.Payment{ChargeID: "m1", OwnerID: 20, Amount: 200, Currency: "XTR", Payload: "sub:20:advanced:0123456789abcdef"})
	_, _ = st.AddPayment(ctx, store.Payment{ChargeID: "m2", OwnerID: 21, Amount: 300, Currency: "XTR", Payload: "sub:21:advanced:0123456789abcdef"})
	if _, m := do(t, "GET", url+"/api/charity", userAuth("20"), ""); m["mine"] != float64(20) {
		t.Fatalf("o'z hissasi (200 × 10%%): %v", m["mine"])
	}
	if _, m := do(t, "GET", url+"/api/charity", userAuth("1"), ""); m["mine"] != float64(0) {
		t.Fatalf("to'lamagan: %v", m["mine"])
	}
}
