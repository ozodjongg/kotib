package billing

import (
	"context"
	"testing"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

func TestLinkedAccountUsesParentPro(t *testing.T) {
	s, st, api, c := newTiered(t)
	ctx := context.Background()
	_ = s.StartTrial(ctx, 1)
	_, _ = st.AddLink(ctx, store.Link{ChildID: 2, ParentID: 1})
	_ = s.StartTrial(ctx, 2)
	if _, err := st.GetSubscription(ctx, 2); err == nil {
		t.Fatal("qo'shimcha akkauntga alohida sinov berilmasligi kerak")
	}
	// Sinovda (Pro funksiyalari ochiq) — ishlaydi, limit umumiy.
	if !s.Entitled(ctx, 2) || !s.HasPro(ctx, 2) || s.Tier(ctx, 2) != TierTrial {
		t.Fatal("sinovda qo'shimcha akkaunt ishlashi kerak")
	}
	s.TrialDailyLimit = 2
	if !s.UseQuota(ctx, 1) || !s.UseQuota(ctx, 2) || s.UseQuota(ctx, 2) {
		t.Fatal("sinov limiti umumiy bo'lishi kerak")
	}
	if !s.CanLink(ctx, 1, 0) || s.CanLink(ctx, 1, MaxLinkedAccounts) || s.CanLink(ctx, 2, 0) {
		t.Fatal("ulash chegarasi")
	}
	// Advanced — qo'shimcha akkaunt to'xtaydi.
	pay(t, s, api, TierAdvanced, "a1", c.t.Add(30*day))
	if s.Entitled(ctx, 2) || s.CanLink(ctx, 1, 0) {
		t.Fatal("Advanced'da qo'shimcha akkaunt ishlamasligi kerak")
	}
	if st2, _ := s.Status(ctx, 2); st2.Active || st2.Tier != TierAdvanced {
		t.Fatalf("status: %+v", st2)
	}
	// Pro — yana ishlaydi, kontekst Pro.
	c.t = c.t.Add(day)
	pay(t, s, api, TierPro, "p1", c.t.Add(30*day))
	if !s.Entitled(ctx, 2) || s.HistoryLimit(ctx, 2) != HistoryPro {
		t.Fatal("Pro'da qo'shimcha akkaunt ishlashi kerak")
	}
}
