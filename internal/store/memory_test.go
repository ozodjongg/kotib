package store

import (
	"context"
	"testing"
	"time"
)

func TestExpireDrafts(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	old := time.Now().Add(-8 * 24 * time.Hour)
	pend, _ := m.CreateDraft(ctx, Draft{OwnerID: 1, Incoming: "x", Reply: "y", CreatedAt: old})
	sent, _ := m.CreateDraft(ctx, Draft{OwnerID: 1, Incoming: "x", Reply: "y", CreatedAt: old})
	_ = m.ResolveDraft(ctx, 1, sent, DraftSent)
	fresh, _ := m.CreateDraft(ctx, Draft{OwnerID: 1, Incoming: "x", Reply: "y"})

	if _, err := m.ExpireDrafts(ctx, time.Now().Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	d, err := m.GetDraft(ctx, 1, pend)
	if err != nil || d.Status != DraftExpired || d.Incoming != "" || d.Reply != "" {
		t.Fatalf("eski pending expired va matnsiz bo'lishi kerak: %+v %v", d, err)
	}
	if _, err := m.GetDraft(ctx, 1, sent); err == nil {
		t.Fatal("eski yuborilgan draft o'chirilishi kerak")
	}
	if d, _ := m.GetDraft(ctx, 1, fresh); d.Status != DraftPending {
		t.Fatal("yangi draftga tegilmasligi kerak")
	}
}

func TestReopenDraftOnlyFromSent(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	id, _ := m.CreateDraft(ctx, Draft{OwnerID: 1})
	if err := m.ReopenDraft(ctx, 1, id); err == nil {
		t.Fatal("pending draftni reopen qilib bo'lmaydi")
	}
	_ = m.ResolveDraft(ctx, 1, id, DraftSent)
	if err := m.ReopenDraft(ctx, 2, id); err == nil {
		t.Fatal("begona egasi reopen qila olmasligi kerak")
	}
	if err := m.ReopenDraft(ctx, 1, id); err != nil {
		t.Fatal(err)
	}
}
