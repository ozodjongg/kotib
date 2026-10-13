// Package store: doimiy saqlash interfeysi. Production uchun Postgres
// (migrations/001_init.sql), dev/test uchun xotiradagi implementatsiya (memory.go).
package store

import (
	"context"
	"errors"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
)

var ErrNotFound = errors.New("topilmadi")

type AccountStatus string

const (
	StatusPending AccountStatus = "pending" // ulanish tugallanmagan
	StatusActive  AccountStatus = "active"
	StatusPaused  AccountStatus = "paused"  // mijoz to'xtatdi
	StatusBroken  AccountStatus = "broken"  // PEER_FLOOD/revoked va h.k. — circuit breaker
	StatusRevoked AccountStatus = "revoked" // mijoz akkauntni uzdi
)

type Account struct {
	OwnerID    int64 // mijozning Telegram user ID'si (Mini App initData'dan)
	Status     AccountStatus
	StatusNote string
	ConsentAt  *time.Time // rozilik vaqti — rozilik bo'lmasa akkaunt ishga tushmaydi
	CreatedAt  time.Time
}

type DraftStatus string

const (
	DraftPending  DraftStatus = "pending"
	DraftSent     DraftStatus = "sent"
	DraftRejected DraftStatus = "rejected"
	DraftExpired  DraftStatus = "expired"
)

type Draft struct {
	ID         int64
	OwnerID    int64
	ChatID     int64 // kontakt user ID
	ReplyToMsg int
	Incoming   string
	Reply      string
	Reason     string // nega draft (rejim yoki guard sababi)
	Status     DraftStatus
	CreatedAt  time.Time
}

type Store interface {
	// Account
	UpsertAccount(ctx context.Context, a Account) error
	GetAccount(ctx context.Context, owner int64) (Account, error)
	SetStatus(ctx context.Context, owner int64, st AccountStatus, note string) error
	ListActiveOwners(ctx context.Context) ([]int64, error)
	// DeleteAccount akkaunt, session, sozlama, draft va auditni o'chiradi ("unutilish huquqi").
	DeleteAccount(ctx context.Context, owner int64) error

	// Session (faqat shifrlangan blob)
	SaveSession(ctx context.Context, owner int64, blob []byte) error
	LoadSession(ctx context.Context, owner int64) ([]byte, error)

	// Settings
	GetSettings(ctx context.Context, owner int64) (config.Settings, error)
	SaveSettings(ctx context.Context, owner int64, s config.Settings) error

	// Drafts
	CreateDraft(ctx context.Context, d Draft) (int64, error)
	GetDraft(ctx context.Context, owner, id int64) (Draft, error)
	ListDrafts(ctx context.Context, owner int64, st DraftStatus, limit int) ([]Draft, error)
	ResolveDraft(ctx context.Context, owner, id int64, st DraftStatus) error
	// ReopenDraft "sent" draftni qayta "pending"ga qaytaradi (yuborish muvaffaqiyatsiz bo'lsa).
	ReopenDraft(ctx context.Context, owner, id int64) error
	// ExpireDrafts "before"dan eski pending draftlarni "expired" qiladi va hal qilingan
	// eski draftlarni (matni bilan) butunlay o'chiradi. O'zgargan qatorlar sonini qaytaradi.
	ExpireDrafts(ctx context.Context, before time.Time) (int64, error)

	// Audit (xabar matnisiz: faqat hodisa va meta)
	Audit(ctx context.Context, owner int64, event, detail string)
	// PruneAudit "before"dan eski audit yozuvlarini o'chiradi.
	PruneAudit(ctx context.Context, before time.Time) (int64, error)
}
