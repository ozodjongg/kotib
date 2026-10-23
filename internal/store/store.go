// Package store: doimiy saqlash interfeysi. Production uchun Postgres
// (migrations/001_init.sql), dev/test uchun xotiradagi implementatsiya (memory.go).
package store

import (
	"context"
	"crypto/sha256"
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
	// Name, Username, SeenAt — Telegram ismi, @username va Mini App'ni oxirgi ochgan vaqti
	// (admin ro'yxati uchun; TouchAccount yozadi, UpsertAccount o'zgartirmaydi).
	Name, Username string
	SeenAt         *time.Time
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

// Link — asosiy akkauntga bog'langan qo'shimcha Telegram akkaunt.
type Link struct {
	ChildID   int64
	ParentID  int64
	Label     string // "Ish", "Do'kon" — bot xabarlari va Mini App'da ko'rinadi
	CreatedAt time.Time
}

// ChatActivity — chatdagi oxirgi faollik (xabar matnisiz): bugun kim yozgani (AI xulosa)
// va javobsiz qolgan chatlar (eslatma). Nol vaqt — hali bo'lmagan.
type ChatActivity struct {
	OwnerID int64
	ChatID  int64
	Name    string // kontakt ismi (Telegram)
	// Private — egasi kotibni bu chatdan chetlatgan (qoida, kontaktda yo'q): AI xulosa va
	// eslatmalarga kirmaydi.
	Private    bool
	LastIn     time.Time // kontaktning oxirgi xabari
	LastOut    time.Time // egasining (yoki kotibning) oxirgi xabari
	RemindedAt time.Time // "javobsiz qoldi" eslatmasi yuborilgan vaqt
}

// Unanswered — kontakt yozgan, lekin hali javob berilmagan.
func (a ChatActivity) Unanswered() bool { return !a.LastIn.IsZero() && a.LastIn.After(a.LastOut) }

type SampleKind string

const (
	SampleShadow SampleKind = "shadow" // soya rejimi: kotib jim tayyorlagan javob va egasi yozgani
	SampleEdit   SampleKind = "edit"   // egasi qoralamani o'zgartirib yubordi
)

// MaxStyleSamples — bitta egada saqlanadigan juftliklar chegarasi (ortig'i yozilmaydi).
const MaxStyleSamples = 200

// StyleSample — o'qitish rejimi uchun juftlik: kotib taxmini va egasi haqiqatda yozgan javob.
type StyleSample struct {
	ID        int64
	OwnerID   int64
	Kind      SampleKind
	Incoming  string // kontakt xabari (kontekst)
	Predicted string
	Actual    string
	CreatedAt time.Time
}

// TokenHash — maxfiy tokenlarning bazadagi ko'rinishi (token o'zi saqlanmaydi).
func TokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// LinkToken — bog'lash havolasi (faqat xesh saqlanadi).
type LinkToken struct {
	ParentID  int64
	Label     string
	ExpiresAt time.Time
}

// ScheduleKind — rejalashtirilgan vazifaning takrorlanishi.
type ScheduleKind string

const (
	ScheduleOnce     ScheduleKind = "once"
	ScheduleHourly   ScheduleKind = "hourly"
	ScheduleDaily    ScheduleKind = "daily"
	ScheduleWeekdays ScheduleKind = "weekdays" // dushanba–juma
	ScheduleWeekly   ScheduleKind = "weekly"   // Weekdays bitmaskidagi kunlar
)

// Schedule — egasi rejalashtirgan AI vazifa: o'sha vaqtda Prompt bajariladi, natija bot chatiga boradi.
type Schedule struct {
	ID       int64
	OwnerID  int64
	Title    string
	Prompt   string
	Kind     ScheduleKind
	At       string // "HH:MM" egasining mintaqasida (hourly — faqat daqiqasi)
	Date     string // "YYYY-MM-DD" (faqat once)
	Weekdays int    // weekly: 1<<time.Weekday bitmaski
	Enabled  bool
	NextRun  *time.Time
	LastRun  *time.Time
	// LastStatus: "" (hali ishlamagan) | running | ok | error.
	LastStatus string
	LastResult string
	Runs       int
	CreatedAt  time.Time
	// TargetID — natija shu kontaktga (egasining Telegram akkauntidan) yuboriladi; 0 — faqat egasiga.
	// TargetAuto — tasdiqsiz yuborish (aks holda egasiga qoralama). TargetName — ko'rsatish uchun.
	TargetID   int64
	TargetName string
	TargetAuto bool
}

// PluginConfig — egasida yoqilgan plagin. Config — shifrlangan JSON (crypto.Vault).
type PluginConfig struct {
	OwnerID   int64
	PluginID  string
	Enabled   bool
	Config    []byte
	UpdatedAt time.Time
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

	// Bir nechta akkaunt (DeleteAccount bularni o'chirmaydi — chaqiruvchi DeleteLink qiladi)
	SaveLinkToken(ctx context.Context, hash []byte, t LinkToken) error
	GetLinkToken(ctx context.Context, hash []byte) (LinkToken, error)
	DeleteLinkToken(ctx context.Context, hash []byte) error
	// AddLink child'ni parent'ga bog'laydi; child avval bog'langan bo'lsa false.
	AddLink(ctx context.Context, l Link) (bool, error)
	// GetLink child akkauntning bog'lanishi (bog'lanmagan bo'lsa ErrNotFound).
	GetLink(ctx context.Context, child int64) (Link, error)
	ListLinks(ctx context.Context, parent int64) ([]Link, error)
	DeleteLink(ctx context.Context, child int64) error

	// ListAuditSince — egasining since'dan keyingi berilgan hodisalari (eskidan yangiga, ko'pi bilan limit ta).
	ListAuditSince(ctx context.Context, owner int64, events []string, since time.Time, limit int) ([]AuditEntry, error)

	// Chat faolligi (xabar matnisiz; DeleteAccount o'chiradi)
	// ChatIn kontakt xabarini qayd etadi: LastIn, Name, Private yangilanadi.
	ChatIn(ctx context.Context, a ChatActivity) error
	// ChatOut egasining (kotibning) xabarini qayd etadi.
	ChatOut(ctx context.Context, owner, chatID int64, at time.Time) error
	// ListChats — since'dan keyin faol bo'lgan chatlar (oxirgi kiruvchi xabar bo'yicha yangidan eskiga, ko'pi bilan 200).
	ListChats(ctx context.Context, owner int64, since time.Time) ([]ChatActivity, error)
	// ListUnanswered — barcha egalar bo'yicha: kontakt before'gacha yozgan, javob berilmagan,
	// shu xabar uchun hali eslatilmagan va Private bo'lmagan chatlar.
	ListUnanswered(ctx context.Context, before time.Time, limit int) ([]ChatActivity, error)
	MarkChatReminded(ctx context.Context, owner, chatID int64, at time.Time) error
	// PruneChats before'dan beri faolligi yo'q chatlarni o'chiradi.
	PruneChats(ctx context.Context, before time.Time) (int64, error)

	// O'qitish rejimi juftliklari (egasida MaxStyleSamples tadan ortig'i yozilmaydi)
	AddStyleSample(ctx context.Context, s StyleSample) error
	// ListStyleSamples — eskidan yangiga, ko'pi bilan limit ta.
	ListStyleSamples(ctx context.Context, owner int64, limit int) ([]StyleSample, error)
	// DeleteStyleSamples egasining ID'si upTo'gacha (shu jumladan) bo'lgan juftliklarini o'chiradi.
	DeleteStyleSamples(ctx context.Context, owner, upTo int64) error
	PruneStyleSamples(ctx context.Context, before time.Time) (int64, error)

	// Avtomatlashtirish havolasi: faqat token xeshi saqlanadi.
	SetHook(ctx context.Context, owner int64, tokenHash []byte) error
	DeleteHook(ctx context.Context, owner int64) error
	HasHook(ctx context.Context, owner int64) (bool, error)
	// OwnerByHook xesh bo'yicha egani topadi (topilmasa ErrNotFound).
	OwnerByHook(ctx context.Context, tokenHash []byte) (int64, error)

	// Rejalashtirilgan vazifalar (DeleteAccount o'chiradi)
	CreateSchedule(ctx context.Context, s Schedule) (int64, error)
	GetSchedule(ctx context.Context, owner, id int64) (Schedule, error)
	// ListSchedules — egasining vazifalari (yaratilish tartibida).
	ListSchedules(ctx context.Context, owner int64) ([]Schedule, error)
	// UpdateSchedule egasi o'zgartiradigan maydonlarni (nom, prompt, jadval, yoqilgan, NextRun, qabul qiluvchi) yozadi.
	UpdateSchedule(ctx context.Context, s Schedule) error
	DeleteSchedule(ctx context.Context, owner, id int64) error
	// ListDueSchedules — yoqilgan va NextRun <= before (barcha egalar, eng eskisidan).
	ListDueSchedules(ctx context.Context, before time.Time, limit int) ([]Schedule, error)
	// ClaimSchedule vazifani bajarish uchun atomar egallaydi: NextRun hali expected bo'lsa, uni next'ga
	// (nil — o'chiriladi) o'tkazadi va holatni "running" qiladi. Boshqa nusxa/oqim egallagan bo'lsa false.
	ClaimSchedule(ctx context.Context, owner, id int64, expected time.Time, next *time.Time, at time.Time) (bool, error)
	// MarkScheduleRunning — qo'lda («Hozir bajarish») ishga tushirish: NextRun o'zgarmaydi;
	// vazifa allaqachon bajarilayotgan bo'lsa false.
	MarkScheduleRunning(ctx context.Context, owner, id int64, at time.Time) (bool, error)
	// FinishSchedule natijani yozadi (status: ok | error) va Runs'ni oshiradi.
	FinishSchedule(ctx context.Context, owner, id int64, status, result string) error

	// Plaginlar (DeleteAccount o'chiradi)
	ListPluginConfigs(ctx context.Context, owner int64) ([]PluginConfig, error)
	SavePluginConfig(ctx context.Context, c PluginConfig) error
	DeletePluginConfig(ctx context.Context, owner int64, pluginID string) error

	// Admin paneli
	// TouchAccount Telegram ismi, username va oxirgi kirish vaqtini yangilaydi (akkaunt yo'q bo'lsa — hech narsa).
	TouchAccount(ctx context.Context, owner int64, name, username string, at time.Time) error
	// ListAccounts — barcha akkauntlar (yangidan eskiga, ko'pi bilan limit ta), sozlamasi, bloki
	// va since'dan keyingi AdminEvents hodisalari soni bilan.
	ListAccounts(ctx context.Context, since time.Time, limit int) ([]AccountRow, error)
	// ListPayments — oxirgi to'lovlar (yangidan eskiga).
	ListPayments(ctx context.Context, limit int) ([]Payment, error)
	// Bloklash (DeleteAccount o'chirmaydi)
	SetBan(ctx context.Context, b Ban) error
	GetBan(ctx context.Context, owner int64) (Ban, error)
	DeleteBan(ctx context.Context, owner int64) error

	// Hayriya (DeleteAccount o'chirmaydi — moliyaviy yozuvlar)
	// CharityStart hisob boshlangan vaqtni qaytaradi; hali yozilmagan bo'lsa candidate'ni saqlaydi.
	CharityStart(ctx context.Context, candidate time.Time) (time.Time, error)
	// PaymentSums — [from, to) oralig'idagi qaytarilmagan to'lovlar: obunalar va xayriyalar (DonationPrefix) alohida.
	PaymentSums(ctx context.Context, from, to time.Time) (subs, donations int64, err error)
	// SubscriberSums — [from, to) oralig'idagi qaytarilmagan obuna to'lovlari (xayriyasiz), ega bo'yicha.
	SubscriberSums(ctx context.Context, from, to time.Time) (map[int64]int64, error)
	// OwnerPaymentSums — egasining [from, to) oralig'idagi qaytarilmagan obuna to'lovlari va xayriyalari.
	OwnerPaymentSums(ctx context.Context, owner int64, from, to time.Time) (subs, donations int64, err error)
	// AddCharityPayout o'tkazmani yozadi; shu oy (monthly) allaqachon yozilgan bo'lsa false.
	AddCharityPayout(ctx context.Context, p CharityPayout) (int64, bool, error)
	// ListCharityPayouts — yangidan eskiga; limit <= 0 — hammasi.
	ListCharityPayouts(ctx context.Context, limit int) ([]CharityPayout, error)
	// MarkCharityPaid o'tkazmani to'langan deb belgilaydi (yo'q yoki allaqachon to'langan bo'lsa ErrNotFound).
	MarkCharityPaid(ctx context.Context, id int64, proof string, admin int64, at time.Time) error
}
