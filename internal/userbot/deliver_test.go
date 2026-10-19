package userbot

import (
	"context"
	"errors"
	"testing"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
)

func deliverEnv(t *testing.T, mutate func(*config.Settings)) (*Manager, *env) {
	t.Helper()
	e := newEnv(t, mutate)
	m := &Manager{Store: e.st}
	m.init(context.Background())
	m.workers[owner] = &worker{tg: e.tg, pipe: e.p}
	return m, e
}

func TestDeliverDraftByDefault(t *testing.T) {
	m, e := deliverEnv(t, nil)
	sent, err := m.Deliver(context.Background(), owner, 100, "Onam", "Xayrli tong", "Xayrli tong, onajon!", false)
	if err != nil || sent || len(e.tg.sent) != 0 {
		t.Fatalf("qoralama kutilgan: sent=%v err=%v", sent, err)
	}
	if ds := e.drafts(); len(ds) != 1 || ds[0].ChatID != 100 || ds[0].Reply != "Xayrli tong, onajon!" {
		t.Fatalf("draft: %+v", ds)
	}
}

func TestDeliverAutoAndSafeguards(t *testing.T) {
	ctx := context.Background()
	m, e := deliverEnv(t, nil)
	if sent, err := m.Deliver(ctx, owner, 100, "Onam", "Tong", "Xayrli tong!", true); err != nil || !sent || len(e.tg.sent) != 1 {
		t.Fatalf("avtomatik: sent=%v err=%v", sent, err)
	}
	// Havola filtrdan o'tmaydi — qoralama.
	if sent, _ := m.Deliver(ctx, owner, 100, "Onam", "Tong", "Mana: https://example.com", true); sent || len(e.drafts()) != 1 {
		t.Fatal("havolali matn avtomatik ketmasligi kerak")
	}
	// O'ziga (Saved Messages) va Telegram xizmatiga yuborilmaydi.
	if _, err := m.Deliver(ctx, owner, owner, "", "x", "x", true); err == nil {
		t.Fatal("o'ziga yuborish")
	}
	// Kunlik limit.
	for i := 0; i < MaxTaskSendsPerDay; i++ {
		e.st.Audit(ctx, owner, "schedule_sent", "chat=100")
	}
	if sent, _ := m.Deliver(ctx, owner, 100, "Onam", "Tong", "Salom", true); sent {
		t.Fatal("kunlik limitdan keyin yuborildi")
	}
	// Sessiya yo'q.
	m2 := &Manager{Store: e.st}
	if _, err := m2.Deliver(ctx, owner, 100, "", "x", "x", true); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("sessiyasiz: %v", err)
	}
}

func TestDeliverRespectsRules(t *testing.T) {
	off := config.ModeOff
	m, e := deliverEnv(t, func(s *config.Settings) {
		s.Rules = []config.Rule{{ID: "r", Match: config.Match{UserIDs: []int64{100}}, Mode: &off}}
	})
	if sent, _ := m.Deliver(context.Background(), owner, 100, "Onam", "x", "Salom", true); sent || len(e.tg.sent) != 0 {
		t.Fatal("qoida o'chirgan kontaktga avtomatik yuborildi")
	}
}
