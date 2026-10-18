package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

func TestGrantAndRevoke(t *testing.T) {
	s, _, _, c := newSvc(t)
	s.Price, s.ProPrice = 150, 250
	ctx := context.Background()
	if s.Entitled(ctx, 1) {
		t.Fatal("obunasiz mijoz")
	}
	if err := s.Grant(ctx, 1, 0, TierPro); err == nil {
		t.Fatal("0 kun qabul qilindi")
	}
	if err := s.Grant(ctx, 1, 30, Tier("vip")); !errors.Is(err, ErrUnknownTier) {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, 1, 30, TierPro); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Status(ctx, 1)
	if !st.Active || !st.Bonus || st.Tier != TierPro || !st.HasPro || st.DaysLeft != 30 {
		t.Fatalf("pro sovg'a: %+v", st)
	}
	// Ikkinchi sovg'a birinchisidan keyin qo'shiladi.
	if err := s.Grant(ctx, 1, 10, TierPro); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Status(ctx, 1); st.DaysLeft != 40 {
		t.Fatalf("40 kun kutilgan: %d", st.DaysLeft)
	}
	c.t = c.t.Add(time.Hour)
	if err := s.RevokeGrant(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Status(ctx, 1); st.Active || st.Tier == TierPro {
		t.Fatalf("bekor qilingan sovg'a: %+v", st)
	}
	if err := s.RevokeGrant(ctx, 1); !errors.Is(err, ErrNoSubscription) {
		t.Fatal(err)
	}
	// Advanced sovg'a Pro ochmaydi.
	if err := s.Grant(ctx, 2, 7, TierAdvanced); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Status(ctx, 2); !st.Active || st.Tier != TierAdvanced || st.HasPro {
		t.Fatalf("advanced sovg'a: %+v", st)
	}
	// To'lov o'chiq bo'lsa sovg'a ma'nosiz.
	off := &Service{Store: store.NewMemory()}
	if err := off.Grant(ctx, 1, 7, TierPro); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
}

func TestBannedLosesService(t *testing.T) {
	ctx := context.Background()
	for _, s := range []*Service{
		{Store: store.NewMemory()}, // to'lov o'chiq — odatda hammaga bepul
		{Store: store.NewMemory(), Price: 100, Exempt: map[int64]bool{1: true}}, // admin ham
	} {
		if !s.Entitled(ctx, 1) || !s.HasPro(ctx, 1) {
			t.Fatal("blokdan oldin xizmat ochiq bo'lishi kerak")
		}
		_ = s.Store.SetBan(ctx, store.Ban{OwnerID: 1, Reason: "spam", By: 9})
		if s.Entitled(ctx, 1) || s.HasPro(ctx, 1) {
			t.Fatal("bloklangan mijozga xizmat")
		}
		if st, _ := s.Status(ctx, 1); st.Active || !st.Banned || st.HasPro {
			t.Fatalf("status: %+v", st)
		}
		_ = s.Store.DeleteBan(ctx, 1)
		if !s.Entitled(ctx, 1) {
			t.Fatal("blok olingach xizmat qaytishi kerak")
		}
	}
	var nilSvc *Service
	if nilSvc.Banned(ctx, 1) {
		t.Fatal("nil servis")
	}
}
