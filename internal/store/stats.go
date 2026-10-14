package store

import (
	"context"
	"time"
)

// SavedPerReply — kotib yuborgan bitta javob egasiga taxminan shuncha vaqt tejaydi
// (bildirishnomani ochish, o'qish, yozish, yuborish).
const SavedPerReply = time.Minute

// SentEvents — kotib orqali yuborilgan javoblar (avtomatik va tasdiqlangan qoralama).
var SentEvents = []string{"auto_sent", "draft_sent"}

// ReplyStats since'dan beri kotib orqali yuborilgan javoblar soni va tejalgan taxminiy vaqt.
func ReplyStats(ctx context.Context, st Store, owner int64, since time.Time) (int, time.Duration, error) {
	n, err := st.CountAudit(ctx, owner, SentEvents, since)
	return n, time.Duration(n) * SavedPerReply, err
}

// MonthStart — loc bo'yicha joriy oyning boshi.
func MonthStart(now time.Time, loc *time.Location) time.Time {
	t := now.In(loc)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
}

// FormatSaved — "~2 soat 15 daqiqa" ko'rinishida.
func FormatSaved(d time.Duration) string {
	m := int(d / time.Minute)
	switch {
	case m < 60:
		return itoa(m) + " daqiqa"
	case m%60 == 0:
		return itoa(m/60) + " soat"
	default:
		return itoa(m/60) + " soat " + itoa(m%60) + " daqiqa"
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
