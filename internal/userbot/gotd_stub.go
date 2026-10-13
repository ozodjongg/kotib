//go:build !gotd

package userbot

import "errors"

// NewGotdConnector gotd'siz yig'ilganda ishlatiladi (testlar/CI uchun).
// Haqiqiy Telegram ulanishi uchun: `go mod tidy && go build -tags gotd ./cmd/secretary`.
func NewGotdConnector(GotdConfig) (Connector, error) {
	return nil, errors.New("Telegram ulanishi mavjud emas: `-tags gotd` bilan yig'ing (README'ga qarang)")
}
