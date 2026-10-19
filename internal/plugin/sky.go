package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// Sky — quyosh chiqishi/botishi va kun uzunligi (Open-Meteo, kalitsiz) hamda oy fazasi (mahalliy
// astronomik hisob, internet kerak emas).
type Sky struct {
	HTTP            *http.Client
	GeoURL, BaseURL string
	Now             func() time.Time // test uchun; nil — time.Now
}

const (
	synodicMonth = 29.530588853 // kun
)

// newMoonRef — ma'lum yangi oy: 2000-01-06 18:14 UTC.
var newMoonRef = time.Date(2000, 1, 6, 18, 14, 0, 0, time.UTC)

// moonAge — oxirgi yangi oydan beri o'tgan kunlar (0…29.53).
func moonAge(t time.Time) float64 {
	d := t.Sub(newMoonRef).Hours() / 24
	return math.Mod(math.Mod(d, synodicMonth)+synodicMonth, synodicMonth)
}

// moonPhase — oy fazasi nomi va yoritilganligi (%).
func moonPhase(t time.Time) (string, float64) {
	age := moonAge(t)
	lit := (1 - math.Cos(2*math.Pi*age/synodicMonth)) / 2 * 100
	names := []string{"🌑 yangi oy", "🌒 o'sib borayotgan o'roqsimon oy", "🌓 birinchi chorak", "🌔 o'sib borayotgan oy",
		"🌕 to'lin oy", "🌖 kamayib borayotgan oy", "🌗 oxirgi chorak", "🌘 kamayib borayotgan o'roqsimon oy"}
	i := int(math.Floor(age/synodicMonth*8+0.5)) % 8
	return names[i], lit
}

// nextPhase — t dan keyingi yangi oy (target 0) yoki to'lin oy (target 0.5) vaqti.
func nextPhase(t time.Time, target float64) time.Time {
	age := moonAge(t)
	left := math.Mod(target*synodicMonth-age+synodicMonth, synodicMonth)
	if left < 0.5 {
		left += synodicMonth // hozir shu fazada: keyingisi
	}
	return t.Add(time.Duration(left * 24 * float64(time.Hour)))
}

func (s *Sky) Info() Info {
	return Info{ID: "sky", Name: "Quyosh va oy", Icon: "🌅", Category: "Kundalik",
		Description: "Quyosh chiqishi va botishi, kun uzunligi, oy fazasi va keyingi to'lin/yangi oy sanasi. Fotograflar, dehqonlar va ro'za tutuvchilar uchun.",
		Fields:      []Field{{Key: "city", Label: "Shahar", Type: FieldText, Default: "Toshkent", Placeholder: "Toshkent"}},
		Examples:    []string{"Har kuni 06:00 da quyosh chiqishi, botishi va kun uzunligi", "Har oy 1-sanasida — shu oydagi to'lin oy va yangi oy sanalari"}}
}

func (s *Sky) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{{Name: "sun_moon", Description: "Shahar uchun quyosh chiqishi/botishi, kun uzunligi (1–7 kun) va oy fazasi, keyingi to'lin va yangi oy.",
		Params: obj(map[string]any{
			"city": str("Shahar nomi (ixtiyoriy, standart — egasining shahri)"),
			"days": map[string]any{"type": "INTEGER", "description": "Necha kun, 1–7, standart 1"},
		})}}
}

func (s *Sky) Call(ctx context.Context, cfg Config, _ string, args map[string]any) (string, error) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	city := argString(args, "city")
	if city == "" {
		city = cfg["city"]
	}
	if city == "" {
		city = "Toshkent"
	}
	loc, err := geocode(ctx, s.HTTP, s.GeoURL, city)
	if err != nil {
		return "", err
	}
	base := s.BaseURL
	if base == "" {
		base = "https://api.open-meteo.com/v1/forecast"
	}
	q := url.Values{
		"latitude": {fmt.Sprint(loc.Latitude)}, "longitude": {fmt.Sprint(loc.Longitude)}, "timezone": {"auto"},
		"daily": {"sunrise,sunset,daylight_duration"}, "forecast_days": {fmt.Sprint(argInt(args, "days", 1, 1, 7))},
	}
	data, err := getBody(ctx, s.HTTP, base+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	var f struct {
		Daily struct {
			Time     []string  `json:"time"`
			Sunrise  []string  `json:"sunrise"`
			Sunset   []string  `json:"sunset"`
			Daylight []float64 `json:"daylight_duration"`
		} `json:"daily"`
	}
	if err := json.Unmarshal(data, &f); err != nil || len(f.Daily.Time) == 0 {
		return "", errors.New("quyosh ma'lumotini olib bo'lmadi")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s), mahalliy vaqt:\n", loc.Name, loc.Country)
	clock := func(v string) string { // "2026-10-19T06:47" → "06:47"
		if i := strings.IndexByte(v, 'T'); i >= 0 {
			return v[i+1:]
		}
		return v
	}
	for i, d := range f.Daily.Time {
		if i >= len(f.Daily.Sunrise) || i >= len(f.Daily.Sunset) {
			break
		}
		fmt.Fprintf(&b, "%s: quyosh chiqishi %s, botishi %s", d, clock(f.Daily.Sunrise[i]), clock(f.Daily.Sunset[i]))
		if i < len(f.Daily.Daylight) {
			mins := int(f.Daily.Daylight[i]/60 + 0.5)
			fmt.Fprintf(&b, ", kun uzunligi %d soat %d daqiqa", mins/60, mins%60)
		}
		b.WriteString("\n")
	}
	t := now()
	phase, lit := moonPhase(t)
	full, nw := nextPhase(t, 0.5).In(uzLoc), nextPhase(t, 0).In(uzLoc)
	fmt.Fprintf(&b, "Oy: %s, yoritilganligi %.0f%%. Keyingi to'lin oy — %s, yangi oy — %s (Toshkent vaqti, taxminan).\n",
		phase, lit, full.Format("02.01.2006 15:04"), nw.Format("02.01.2006 15:04"))
	return b.String(), nil
}
