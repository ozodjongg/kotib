package store

import (
	"context"
	"testing"
	"time"
)

type fixedAudit struct {
	*Memory
	rows []AuditEntry
}

func (f fixedAudit) ListAuditSince(_ context.Context, _ int64, _ []string, since time.Time, _ int) ([]AuditEntry, error) {
	var out []AuditEntry
	for _, r := range f.rows {
		if !r.At.Before(since) {
			out = append(out, r)
		}
	}
	return out, nil
}

func TestExtendedStats(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Tashkent")
	now := time.Date(2026, 10, 15, 18, 0, 0, 0, loc)
	at := func(daysAgo, hour int) time.Time {
		return time.Date(2026, 10, 15-daysAgo, hour, 10, 0, 0, loc)
	}
	rows := []AuditEntry{
		{At: at(0, 14), Event: "draft_created", Detail: "id=1 chat=100"},
		{At: at(0, 14), Event: "draft_sent", Detail: "1"},
		{At: at(0, 15), Event: "auto_sent", Detail: "chat=100"},
		{At: at(1, 14), Event: "draft_created", Detail: "id=2 chat=200"},
		{At: at(1, 14), Event: "draft_rejected", Detail: "2"},
		{At: at(2, 9), Event: "draft_created", Detail: "id=3 chat=100"},
		{At: at(2, 9), Event: "draft_sent", Detail: "3"},
		{At: at(3, 9), Event: "skip", Detail: "tinch soatlar"},
		{At: at(4, 9), Event: "blocked", Detail: "injection"},
		{At: at(8, 9), Event: "auto_sent", Detail: "chat=300"},  // o'tgan davr
		{At: at(20, 9), Event: "auto_sent", Detail: "chat=300"}, // davrdan tashqarida
	}
	st, err := ExtendedStats(context.Background(), fixedAudit{NewMemory(), rows}, 1, now, 7, loc)
	if err != nil {
		t.Fatal(err)
	}
	if st.Prepared != 4 || st.Sent != 3 || st.AutoSent != 1 || st.DraftSent != 2 || st.Rejected != 1 ||
		st.Skipped != 1 || st.Blocked != 1 || st.SavedMin != 3 {
		t.Fatalf("jami: %+v", st)
	}
	if st.Approval != 67 { // 2 / 3
		t.Fatalf("tasdiqlash: %d", st.Approval)
	}
	if len(st.Series) != 7 || st.Series[6].Day != "2026-10-15" || st.Series[6].Prepared != 2 || st.Series[6].Sent != 2 || st.Series[5].Prepared != 1 {
		t.Fatalf("kunlar: %+v", st.Series)
	}
	if st.Hours[14] != 2 || st.BusiestHour() != 14 {
		t.Fatalf("soatlar: %v", st.Hours)
	}
	if len(st.TopChats) != 2 || st.TopChats[0].ChatID != 100 || st.TopChats[0].Count != 3 {
		t.Fatalf("top: %+v", st.TopChats)
	}
	if st.PrevPrepared != 1 || st.PrevSent != 1 {
		t.Fatalf("o'tgan davr: %d %d", st.PrevPrepared, st.PrevSent)
	}
	empty, _ := ExtendedStats(context.Background(), fixedAudit{NewMemory(), nil}, 1, now, 30, loc)
	if empty.Approval != -1 || empty.BusiestHour() != -1 || len(empty.Series) != 30 {
		t.Fatalf("bo'sh: %+v", empty)
	}
	if ChatFromDetail("id=5 chat=-42") != -42 || ChatFromDetail("5") != 0 {
		t.Fatal("detail")
	}
}
