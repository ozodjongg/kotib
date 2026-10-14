package store

import (
	"context"
	"testing"
	"time"
)

func TestFormatSaved(t *testing.T) {
	cases := map[time.Duration]string{
		0: "0 daqiqa", 45 * time.Minute: "45 daqiqa", 2 * time.Hour: "2 soat", 143 * time.Minute: "2 soat 23 daqiqa",
	}
	for d, want := range cases {
		if got := FormatSaved(d); got != want {
			t.Errorf("%v: %q, kutilgan %q", d, got, want)
		}
	}
}

func TestReplyStats(t *testing.T) {
	st := NewMemory()
	ctx := context.Background()
	st.Audit(ctx, 1, "auto_sent", "")
	st.Audit(ctx, 1, "draft_sent", "")
	st.Audit(ctx, 1, "draft_created", "")
	n, saved, err := ReplyStats(ctx, st, 1, time.Now().Add(-time.Hour))
	if err != nil || n != 2 || saved != 2*SavedPerReply {
		t.Fatalf("%d %v %v", n, saved, err)
	}
	loc := time.FixedZone("UZ", 5*3600)
	if ms := MonthStart(time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC), loc); ms.Day() != 1 || ms.Month() != 10 {
		t.Fatal(ms)
	}
}
