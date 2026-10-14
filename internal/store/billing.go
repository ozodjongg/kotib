package store

import "time"

// Subscription — mijozning obuna holati (Telegram Stars). Nol vaqt = belgilanmagan.
type Subscription struct {
	OwnerID     int64
	TrialUntil  time.Time
	PaidUntil   time.Time
	ChargeID    string // joriy obunaning oxirgi telegram_payment_charge_id'si
	Canceled    bool   // avtomatik uzaytirish o'chirilgan (muddat oxirigacha amal qiladi)
	RemindedFor time.Time
	// BonusUntil — referal mukofoti: shu vaqtgacha to'langan obuna kabi to'liq xizmat
	// (Telegram pul yechmaydi). Avtomatik yangilanadigan obunada to'langan davrdan keyin turadi.
	BonusUntil time.Time
}

// Until — xizmatdan foydalanish mumkin bo'lgan oxirgi vaqt (sinov, to'langan yoki bonus).
func (s Subscription) Until() time.Time {
	u := s.TrialUntil
	if s.PaidUntil.After(u) {
		u = s.PaidUntil
	}
	if s.BonusUntil.After(u) {
		u = s.BonusUntil
	}
	return u
}

// Premium — now paytida to'liq (to'langan yoki bonus) xizmat: sinov cheklovlarisiz.
func (s Subscription) Premium(now time.Time) bool {
	return s.PaidUntil.After(now) || s.BonusUntil.After(now)
}

// Referral — kim kimni taklif qilgan. Nol vaqt = hali bo'lmagan bosqich.
type Referral struct {
	InviteeID   int64
	ReferrerID  int64
	CreatedAt   time.Time
	ActivatedAt time.Time // do'st Telegram akkauntini uladi
	PaidAt      time.Time // do'stning birinchi to'lovi
}

// ReferralStage — mukofot beriladigan bosqich.
type ReferralStage string

const (
	ReferralActivated ReferralStage = "activated"
	ReferralPaid      ReferralStage = "paid"
)

// ReferralStats — bitta taklif qiluvchining natijasi.
type ReferralStats struct {
	Invited   int `json:"invited"`
	Activated int `json:"activated"`
	Paid      int `json:"paid"`
}

// Payment — bitta muvaffaqiyatli Stars to'lovi.
type Payment struct {
	ChargeID  string
	OwnerID   int64
	Amount    int
	Currency  string
	Payload   string
	Recurring bool
	Refunded  bool
	CreatedAt time.Time
}

// Stats — admin uchun umumiy ko'rsatkichlar.
type Stats struct {
	Accounts   int
	Active     int
	PaidActive int
	StarsTotal int64 // qaytarilmagan to'lovlar yig'indisi
	Referrals  int   // taklif orqali kelganlar
	RefPaid    int   // ulardan to'laganlar
}

// AuditEntry — mijozga ko'rsatiladigan faollik yozuvi (xabar matnisiz).
type AuditEntry struct {
	At     time.Time
	Event  string
	Detail string
}
