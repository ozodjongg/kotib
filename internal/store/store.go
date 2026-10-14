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

// GoogleLink — mijozning Google akkaunti (OAuth). Token — shifrlangan refresh token.
type GoogleLink struct {
	OwnerID   int64
	Email     string
	Token     []byte
	CreatedAt time.Time
}

type ActionKind string

const (
	ActionEvent ActionKind = "event" // Google Calendar tadbiri
	ActionTask  ActionKind = "task"  // Google Tasks vazifasi
)

type ActionStatus string

const (
	ActionPending  ActionStatus = "pending"
	ActionDone     ActionStatus = "done"
	ActionRejected ActionStatus = "rejected"
	ActionFailed   ActionStatus = "failed"
)

// Action — kotib taklif qilgan, egasi tasdiqlashi kerak bo'lgan amal.
// Payload — turga mos JSON (google.Event / google.Task).
type Action struct {
	ID        int64
	OwnerID   int64
	Kind      ActionKind
	Payload   []byte
	Status    ActionStatus
	CreatedAt time.Time
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
	// ListAudit egasining oxirgi audit yozuvlari (yangidan eskiga).
	ListAudit(ctx context.Context, owner int64, limit int) ([]AuditEntry, error)

	// Billing (DeleteAccount bularni o'chirmaydi — moliyaviy yozuvlar)
	GetSubscription(ctx context.Context, owner int64) (Subscription, error)
	SaveSubscription(ctx context.Context, s Subscription) error
	// AddPayment to'lovni yozadi; shu charge_id avval yozilgan bo'lsa false qaytaradi.
	AddPayment(ctx context.Context, p Payment) (bool, error)
	GetPayment(ctx context.Context, chargeID string) (Payment, error)
	// MarkRefunded to'lovni qaytarilgan deb belgilaydi (allaqachon qaytarilgan bo'lsa ErrNotFound).
	MarkRefunded(ctx context.Context, chargeID string) error
	// ListSubscriptionsEnding — tugash vaqti (Until) [from, to) oralig'ida bo'lgan obunalar.
	ListSubscriptionsEnding(ctx context.Context, from, to time.Time) ([]Subscription, error)
	Stats(ctx context.Context, now time.Time) (Stats, error)
	// UseDaily — day kuni (YYYY-MM-DD) uchun hisobni atomar bittaga oshiradi, agar u
	// limit'dan kichik bo'lsa; limitga yetgan bo'lsa hech narsa o'zgarmaydi va false.
	UseDaily(ctx context.Context, owner int64, day string, limit int) (bool, error)

	// Google (DeleteAccount bularni ham o'chiradi)
	SaveGoogleLink(ctx context.Context, l GoogleLink) error
	GetGoogleLink(ctx context.Context, owner int64) (GoogleLink, error)
	DeleteGoogleLink(ctx context.Context, owner int64) error
	CreateAction(ctx context.Context, a Action) (int64, error)
	GetAction(ctx context.Context, owner, id int64) (Action, error)
	// SetActionStatus holatni from → to o'zgartiradi (atomar); holat from bo'lmasa ErrNotFound —
	// bir tugmani ikki marta bosish ikki tadbir yaratmasin.
	SetActionStatus(ctx context.Context, owner, id int64, from, to ActionStatus) error
	// PruneActions "before"dan eski amallarni o'chiradi.
	PruneActions(ctx context.Context, before time.Time) (int64, error)
}
