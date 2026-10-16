package userbot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/plugin"
)

type feedTG struct {
	Telegram // boshqa metodlar ishlatilmaydi
	kind     string
}

func (f *feedTG) Feed(_ context.Context, kind string, _ time.Time, _ int) ([]plugin.FeedChat, error) {
	f.kind = kind
	return []plugin.FeedChat{{Title: "Kanal"}}, nil
}

func TestManagerFeed(t *testing.T) {
	m, _ := newManager(&fakeConn{})
	ctx := context.Background()
	if _, err := m.Feed(ctx, 7, "channels", time.Now(), 5); !errors.Is(err, ErrNoLiveSession) {
		t.Fatalf("sessiyasiz: %v", err)
	}
	f := &feedTG{}
	m.mu.Lock()
	m.workers[7] = &worker{tg: f}
	m.mu.Unlock()
	chats, err := m.Feed(ctx, 7, "groups", time.Now(), 5)
	if err != nil || len(chats) != 1 || f.kind != "groups" {
		t.Fatalf("%v %v %q", chats, err, f.kind)
	}
	if _, err := m.Feed(ctx, 8, "groups", time.Now(), 5); !errors.Is(err, ErrNoLiveSession) {
		t.Fatal("boshqa egasining sessiyasi ishlatilmasligi kerak")
	}
	m.mu.Lock()
	delete(m.workers, 7)
	m.mu.Unlock()
}
