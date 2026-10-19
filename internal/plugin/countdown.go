package plugin

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// Countdown — egasining muhim sanalarigacha qolgan kunlar (to'y, imtihon, to'lov, tug'ilgan kun).
// Sanalar sozlamada; yilsiz sana (15.11) — har yili takrorlanadi. Internet kerak emas.
type Countdown struct {
	Now func() time.Time // test uchun; nil — time.Now
}

// countdownKeys — sozlamadagi sana maydonlari.
var countdownKeys = []string{"event1", "event2", "event3", "event4", "event5"}

type countdownEvent struct {
	name   string
	date   time.Time // yillik sanada — joriy yildagisi
	yearly bool
}

// parseEvent — "To'y, 15.11.2026" yoki "Onamning tug'ilgan kuni, 03.05" (har yili).
func parseEvent(v string) (countdownEvent, error) {
	i := strings.LastIndex(v, ",")
	if i <= 0 {
		return countdownEvent{}, &ErrInvalid{"sana formati: «Nomi, KK.OO.YYYY» yoki har yilgi uchun «Nomi, KK.OO»"}
	}
	name, ds := strings.TrimSpace(v[:i]), strings.TrimSpace(v[i+1:])
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return countdownEvent{}, &ErrInvalid{"sana nomi bo'sh yoki juda uzun"}
	}
	if t, err := time.ParseInLocation("02.01.2006", ds, uzLoc); err == nil {
		return countdownEvent{name: name, date: t}, nil
	}
	if t, err := time.ParseInLocation("02.01", ds, uzLoc); err == nil {
		return countdownEvent{name: name, date: t, yearly: true}, nil
	}
	return countdownEvent{}, &ErrInvalid{"«" + name + "»: sana noto'g'ri — KK.OO.YYYY (masalan 15.11.2026) yoki KK.OO"}
}

func (c *Countdown) Info() Info {
	fields := make([]Field, len(countdownKeys))
	for i, k := range countdownKeys {
		fields[i] = Field{Key: k, Label: fmt.Sprintf("%d-sana", i+1), Type: FieldText, Placeholder: "To'y, 15.11.2026"}
	}
	fields[0].Required = true
	fields[0].Help = "Nomi va sanasi vergul bilan: «Imtihon, 01.12.2026». Yilsiz sana (masalan «Onamning tug'ilgan kuni, 03.05») har yili takrorlanadi."
	return Info{ID: "countdown", Name: "Sanoq", Icon: "⏳", Category: "Kundalik",
		Description: "Muhim sanalaringizgacha qancha qolgani: to'y, imtihon, kredit to'lovi, tug'ilgan kunlar. Har kuni «12 kun qoldi» deb eslatib turadi.",
		Fields:      fields,
		Examples:    []string{"Har kuni 08:00 da sanalarimgacha qancha qolgani — 7 kundan kam qolganlarini ta'kidla", "Har dushanba — shu oydagi muhim sanalar"}}
}

func (c *Countdown) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{{Name: "countdown", Description: "Egasining muhim sanalari va ularga necha kun qolgani (yaqinidan boshlab); o'tib ketganlari ham ko'rsatiladi.",
		Params: obj(map[string]any{})}}
}

func (c *Countdown) Validate(cfg Config) error {
	for _, k := range countdownKeys {
		if v := cfg[k]; v != "" {
			if _, err := parseEvent(v); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Countdown) Call(_ context.Context, cfg Config, _ string, _ map[string]any) (string, error) {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	t := now().In(uzLoc)
	today := day(t.Year(), t.Month(), t.Day())
	type row struct {
		ev   countdownEvent
		date time.Time
		days int
	}
	var rows []row
	for _, k := range countdownKeys {
		if cfg[k] == "" {
			continue
		}
		ev, err := parseEvent(cfg[k])
		if err != nil {
			continue
		}
		d := day(ev.date.Year(), ev.date.Month(), ev.date.Day())
		if ev.yearly {
			d = day(today.Year(), ev.date.Month(), ev.date.Day())
			if d.Before(today) {
				d = day(today.Year()+1, ev.date.Month(), ev.date.Day())
			}
		}
		rows = append(rows, row{ev, d, int(math.Round(d.Sub(today).Hours() / 24))})
	}
	if len(rows) == 0 {
		return "Sanalar kiritilmagan (Plaginlar → Sanoq → Sozlash).", nil
	}
	// Kelgusilari yaqinidan, o'tib ketganlari oxirida (eng yaqinda o'tgani birinchi).
	sort.SliceStable(rows, func(i, j int) bool {
		a, c := rows[i].days, rows[j].days
		if (a < 0) != (c < 0) {
			return a >= 0
		}
		if a >= 0 {
			return a < c
		}
		return a > c
	})
	var b strings.Builder
	fmt.Fprintf(&b, "Bugun %s, %s.\n", today.Format("02.01.2006"), uzWeekday(today))
	for _, r := range rows {
		when := r.date.Format("02.01.2006") + ", " + uzWeekday(r.date)
		yearly := ""
		if r.ev.yearly {
			yearly = " (har yili)"
		}
		switch {
		case r.days == 0:
			fmt.Fprintf(&b, "🎉 %s — BUGUN (%s)%s\n", r.ev.name, when, yearly)
		case r.days == 1:
			fmt.Fprintf(&b, "⏳ %s — ertaga (%s)%s\n", r.ev.name, when, yearly)
		case r.days > 0:
			fmt.Fprintf(&b, "⏳ %s — %d kun qoldi (%s)%s\n", r.ev.name, r.days, when, yearly)
		default:
			fmt.Fprintf(&b, "✔️ %s — %d kun oldin o'tdi (%s)\n", r.ev.name, -r.days, when)
		}
	}
	return b.String(), nil
}
