package userbot

import (
	"context"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/plugin"
)

// feeder — kanal va guruh xabarlarini o'qiy oladigan Telegram ulanishi (gotdTG).
type feeder interface {
	Feed(ctx context.Context, kind string, since time.Time, maxChats int) ([]plugin.FeedChat, error)
}

// Feed — «Telegram kanallar va guruhlar» plagini uchun: egasining kanal (kind=channels) yoki
// guruh (kind=groups) xabarlari since'dan beri. Sessiya ulanmagan bo'lsa ErrNoLiveSession.
func (m *Manager) Feed(ctx context.Context, owner int64, kind string, since time.Time, maxChats int) ([]plugin.FeedChat, error) {
	m.mu.Lock()
	w := m.workers[owner]
	m.mu.Unlock()
	if w == nil {
		return nil, ErrNoLiveSession
	}
	f, ok := w.telegram().(feeder)
	if !ok {
		return nil, ErrNoLiveSession
	}
	return f.Feed(ctx, kind, since, maxChats)
}
