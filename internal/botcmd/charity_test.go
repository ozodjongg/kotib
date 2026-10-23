package botcmd

import (
	"context"
	"strings"
	"testing"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

func TestCharityCommand(t *testing.T) {
	h, st, _, notes := setup(t)
	ctx := context.Background()
	if r := h.Command(ctx, 1, "hayriya", ""); !strings.Contains(r.Text, "yoqilmagan") {
		t.Fatalf("o'chiq: %q", r.Text)
	}
	h.Billing.CharityPercent = 10
	r := h.Command(ctx, 1, "hayriya", "")
	if !strings.Contains(r.Text, "10%") || len(r.Links) != len(donationPresets) || strings.Contains(r.Text, "Admin:") {
		t.Fatalf("hisobot: %+v", r)
	}
	if r := h.Command(ctx, 1, "hayriya", "250"); r.ButtonURL == "" || !strings.Contains(r.Button, "250") {
		t.Fatalf("summa bilan: %+v", r)
	}
	if r := h.Command(ctx, 1, "hayriya", "0"); r.ButtonURL != "" {
		t.Fatalf("0 ⭐ bo'lmaydi: %+v", r)
	}
	if r := h.Command(ctx, 1, "hayriya", "manual 100 naqd"); r.ButtonURL != "" || strings.Contains(r.Text, "yozildi") {
		t.Fatalf("oddiy foydalanuvchi admin amalini qila olmaydi: %+v", r)
	}
	if r := h.Command(ctx, 99, "hayriya", ""); !strings.Contains(r.Text, "Admin:") {
		t.Fatalf("admin yordami: %q", r.Text)
	}
	if r := h.Command(ctx, 99, "hayriya", "manual 1000 Mehr fondiga naqd"); !strings.Contains(r.Text, "yozildi") {
		t.Fatalf("manual: %q", r.Text)
	}
	id, _, _ := st.AddCharityPayout(ctx, store.CharityPayout{Kind: store.CharityMonthly, Period: "2026-09", Stars: 30})
	if r := h.Command(ctx, 99, "hayriya", "paid #"+itoa(id)); !strings.Contains(r.Text, "Foydalanish") {
		t.Fatalf("isbotsiz: %q", r.Text)
	}
	if r := h.Command(ctx, 99, "hayriya", "paid #"+itoa(id)+" EQtx123"); !strings.Contains(r.Text, "belgilandi") {
		t.Fatalf("paid: %q", r.Text)
	}
	rep, _ := h.Billing.CharityReport(ctx)
	if rep.PaidStars != 1030 {
		t.Fatalf("hisobot: %+v", rep)
	}

	// Obuna to'lovidan keyin ulush, xayriyadan keyin — rahmat.
	h.Paid(ctx, 1, billing.PaymentInfo{Currency: "XTR", Amount: 50, ChargeID: "c1", Payload: "sub:1:0123456789abcdef"})
	h.Paid(ctx, 1, billing.PaymentInfo{Currency: "XTR", Amount: 70, ChargeID: "d1", Payload: "don:1:0123456789abcdef"})
	got := strings.Join(*notes, "\n---\n")
	if !strings.Contains(got, "5 ⭐ hayriyaga ajratildi") || !strings.Contains(got, "70 ⭐ hayriyaga qo'shildi") {
		t.Fatalf("xabarlar: %s", got)
	}
	if r := h.Command(ctx, 99, "refund", "d1"); !strings.Contains(r.Text, "Qaytarildi") ||
		!strings.Contains((*notes)[len(*notes)-1], "hayriya xayriyasi") {
		t.Fatalf("xayriya refund: %q %v", r.Text, *notes)
	}
}
