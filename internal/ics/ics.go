// Package ics: Google'siz taqvim — tasdiqlangan uchrashuv uchun .ics fayl (RFC 5545).
// iPhone (Apple Taqvim) va Android (istalgan taqvim ilovasi) havolani ochganda
// "Taqvimga qo'shish" oynasini o'zi ko'rsatadi. Havola HMAC bilan imzolangan.
package ics

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Event — .ics uchun tadbir.
type Event struct {
	UID      string
	Title    string
	Start    time.Time
	Duration time.Duration
	Note     string
}

// Build VCALENDAR matnini yaratadi (15 daqiqa oldin eslatma bilan).
func Build(e Event, now time.Time) []byte {
	const ts = "20060102T150405Z"
	var b strings.Builder
	line := func(s string) { b.WriteString(fold(s)); b.WriteString("\r\n") }
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//Kotib//Telegram kotibi//UZ")
	line("CALSCALE:GREGORIAN")
	line("METHOD:PUBLISH")
	line("BEGIN:VEVENT")
	line("UID:" + escape(e.UID))
	line("DTSTAMP:" + now.UTC().Format(ts))
	line("DTSTART:" + e.Start.UTC().Format(ts))
	line("DTEND:" + e.Start.Add(e.Duration).UTC().Format(ts))
	line("SUMMARY:" + escape(e.Title))
	if e.Note != "" {
		line("DESCRIPTION:" + escape(e.Note))
	}
	line("BEGIN:VALARM")
	line("TRIGGER:-PT15M")
	line("ACTION:DISPLAY")
	line("DESCRIPTION:" + escape(e.Title))
	line("END:VALARM")
	line("END:VEVENT")
	line("END:VCALENDAR")
	return []byte(b.String())
}

// escape — RFC 5545 TEXT qiymati.
func escape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`)
	return r.Replace(s)
}

// fold 75 baytdan uzun qatorlarni bo'ladi (UTF-8 belgini o'rtasidan kesmasdan).
func fold(s string) string {
	if len(s) <= 75 {
		return s
	}
	var b strings.Builder
	n, limit := 0, 75
	for _, r := range s {
		w := len(string(r))
		if n+w > limit {
			b.WriteString("\r\n ")
			n, limit = 0, 74 // davom qatorining boshidagi bo'sh joy ham hisobga kiradi
		}
		b.WriteRune(r)
		n += w
	}
	return b.String()
}

// Signer — .ics havolalarini imzolaydi va tekshiradi.
type Signer struct {
	Key  []byte
	Base string // https://domen
}

func (s *Signer) sig(owner, id int64) string {
	m := hmac.New(sha256.New, s.Key)
	fmt.Fprintf(m, "ics:%d:%d", owner, id)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

// URL — egasi va amal uchun imzolangan havola.
func (s *Signer) URL(owner, id int64) string {
	return fmt.Sprintf("%s/ics/%d/%d/%s.ics", strings.TrimRight(s.Base, "/"), owner, id, s.sig(owner, id))
}

// Verify yo'l qismlarini tekshiradi.
func (s *Signer) Verify(ownerS, idS, sig string) (owner, id int64, ok bool) {
	owner, err1 := strconv.ParseInt(ownerS, 10, 64)
	id, err2 := strconv.ParseInt(idS, 10, 64)
	if err1 != nil || err2 != nil || owner <= 0 || id <= 0 {
		return 0, 0, false
	}
	sig = strings.TrimSuffix(sig, ".ics")
	return owner, id, hmac.Equal([]byte(sig), []byte(s.sig(owner, id)))
}
