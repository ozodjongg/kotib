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

type FollowupStatus string

const (
	FollowupPending  FollowupStatus = "pending"  // muddat kelmagan
	FollowupReminded FollowupStatus = "reminded" // egasi yozmagan, eslatildi
	FollowupDone     FollowupStatus = "done"     // egasi o'zi yozdi
)

// Followup — kotib "keyinroq yozaman" degan chat (va'da). Hash — kotib javobining xeshi:
// muddat kelganda chatdagi egasining oxirgi xabari shu bo'lsa, egasi hali yozmagan.
type Followup struct {
	OwnerID   int64
	ChatID    int64
	Hash      string
	DueAt     time.Time
	Status    FollowupStatus
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

	// Referal (DeleteAccount bularni o'chirmaydi — mukofotni qayta olib bo'lmasin)
	// EnsureReferralCode egasining kodini qaytaradi; kodi bo'lmasa candidate'ni saqlaydi.
	// candidate boshqa egada band bo'lsa ErrNotFound (chaqiruvchi yangi kod bilan qayta urinadi).
	EnsureReferralCode(ctx context.Context, owner int64, candidate string) (string, error)
	// ReferrerByCode kod egasini topadi (topilmasa ErrNotFound).
	ReferrerByCode(ctx context.Context, code string) (int64, error)
	// AddReferral taklifni yozadi; invitee avval taklif qilingan bo'lsa false.
	AddReferral(ctx context.Context, invitee, referrer int64) (bool, error)
	GetReferral(ctx context.Context, invitee int64) (Referral, error)
	// MarkReferral bosqichni bir marta belgilaydi: avval belgilangan bo'lsa (yoki taklif yo'q) false.
	MarkReferral(ctx context.Context, invitee int64, stage ReferralStage, at time.Time) (bool, error)
	ReferralStats(ctx context.Context, referrer int64) (ReferralStats, error)

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

	// CountAudit egasining since'dan keyingi berilgan hodisalari soni (statistika uchun).
	CountAudit(ctx context.Context, owner int64, events []string, since time.Time) (int, error)

	// Va'dalar (har chatda bittadan; yangisi eskisini almashtiradi)
	SaveFollowup(ctx context.Context, f Followup) error
	// ListDueFollowups — muddati before'dan oldin kelgan pending va'dalar (barcha egalar).
	ListDueFollowups(ctx context.Context, before time.Time, limit int) ([]Followup, error)
	// ListOpenFollowups — egasining hali yopilmagan (pending/reminded) va'dalari.
	ListOpenFollowups(ctx context.Context, owner int64) ([]Followup, error)
	SetFollowupStatus(ctx context.Context, owner, chatID int64, st FollowupStatus) error
	PruneFollowups(ctx context.Context, before time.Time) (int64, error)

	// Avtomatlashtirish havolasi: faqat token xeshi saqlanadi.
	SetHook(ctx context.Context, owner int64, tokenHash []byte) error
	DeleteHook(ctx context.Context, owner int64) error
	HasHook(ctx context.Context, owner int64) (bool, error)
	// OwnerByHook xesh bo'yicha egani topadi (topilmasa ErrNotFound).
	OwnerByHook(ctx context.Context, tokenHash []byte) (int64, error)
}
