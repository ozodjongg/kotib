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
}

// Until — xizmatdan foydalanish mumkin bo'lgan oxirgi vaqt (sinov yoki to'langan).
func (s Subscription) Until() time.Time {
	if s.PaidUntil.After(s.TrialUntil) {
		return s.PaidUntil
	}
	return s.TrialUntil
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
}

// AuditEntry — mijozga ko'rsatiladigan faollik yozuvi (xabar matnisiz).
type AuditEntry struct {
	At     time.Time
	Event  string
	Detail string
}
