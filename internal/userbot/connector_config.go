package userbot

import (
	"log/slog"

	"github.com/ozodjongg/secretar_ai_agent/internal/crypto"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// GotdConfig gotd/td asosidagi Connector sozlamalari.
type GotdConfig struct {
	AppID   int    // https://my.telegram.org dan
	AppHash string // https://my.telegram.org dan
	Store   store.Store
	Vault   *crypto.Vault
	// Proxy — ixtiyoriy SOCKS5 ("socks5://user:pass@host:port"). Barqaror IP uchun tavsiya etiladi.
	Proxy string
	Log   *slog.Logger
}
