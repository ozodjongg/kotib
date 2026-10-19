package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// Air — havo sifati (Open-Meteo Air Quality, kalitsiz): AQI (AQSh shkalasi), PM2.5, PM10 va
// bugun/ertaga eng yomon soat — kotib ogohlantirish va maslahat beradi.
type Air struct {
	HTTP            *http.Client
	GeoURL, BaseURL string
}

func (a *Air) Info() Info {
	return Info{ID: "air", Name: "Havo sifati", Icon: "🌫", Category: "Kundalik",
		Description: "Shahringiz havosining ifloslanishi (AQI, PM2.5, PM10) va ertangi prognoz: havo yomon bo'lsa, kotib ogohlantiradi va maslahat beradi (Open-Meteo, kalit shart emas).",
		Fields:      []Field{{Key: "city", Label: "Shahar", Type: FieldText, Default: "Toshkent", Placeholder: "Toshkent"}},
		Examples:    []string{"Har kuni 07:30 da havo sifati — iflos bo'lsa niqob va sayr haqida maslahat", "Har kuni 21:00 da ertangi havo prognozi"}}
}

func (a *Air) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{{Name: "air_quality", Description: "Shahar havosi sifati: hozirgi AQI (AQSh shkalasi), PM2.5, PM10, daraja va bugun/ertaga eng yuqori AQI.",
		Params: obj(map[string]any{"city": str("Shahar nomi (ixtiyoriy, standart — egasining shahri)")})}}
}

// aqiLevel — AQSh AQI shkalasi bo'yicha daraja va qisqa maslahat.
func aqiLevel(v float64) (string, string) {
	switch {
	case v <= 50:
		return "yaxshi 🟢", "havo toza, sayr qilish mumkin"
	case v <= 100:
		return "o'rtacha 🟡", "sezgir odamlar uzoq vaqt ochiq havoda bo'lmasin"
	case v <= 150:
		return "sezgir guruhlar uchun zararli 🟠", "bolalar, keksalar va astma bilan og'riganlar ochiq havoda kam bo'lsin"
	case v <= 200:
		return "zararli 🔴", "ochiq havoda sport qilmang, tashqarida niqob taqing, derazalarni yoping"
	case v <= 300:
		return "juda zararli 🟣", "imkon qadar uyda qoling, havo tozalagichdan foydalaning, tashqarida N95 niqob"
	}
	return "xavfli 🟤", "tashqariga chiqmang, derazalarni yoping"
}

func (a *Air) Call(ctx context.Context, cfg Config, _ string, args map[string]any) (string, error) {
	city := argString(args, "city")
	if city == "" {
		city = cfg["city"]
	}
	if city == "" {
		city = "Toshkent"
	}
	loc, err := geocode(ctx, a.HTTP, a.GeoURL, city)
	if err != nil {
		return "", err
	}
	base := a.BaseURL
	if base == "" {
		base = "https://air-quality-api.open-meteo.com/v1/air-quality"
	}
	q := url.Values{
		"latitude": {fmt.Sprint(loc.Latitude)}, "longitude": {fmt.Sprint(loc.Longitude)}, "timezone": {"auto"},
		"current": {"us_aqi,pm2_5,pm10"}, "hourly": {"us_aqi"}, "forecast_days": {"2"},
	}
	data, err := getBody(ctx, a.HTTP, base+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	var f struct {
		Current struct {
			AQI  *float64 `json:"us_aqi"`
			PM25 float64  `json:"pm2_5"`
			PM10 float64  `json:"pm10"`
		} `json:"current"`
		Hourly struct {
			Time []string   `json:"time"`
			AQI  []*float64 `json:"us_aqi"`
		} `json:"hourly"`
	}
	if err := json.Unmarshal(data, &f); err != nil || f.Current.AQI == nil {
		return "", errors.New("havo sifati ma'lumotini olib bo'lmadi")
	}
	level, advice := aqiLevel(*f.Current.AQI)
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s). Hozir AQI %.0f — %s. PM2.5: %.0f µg/m³, PM10: %.0f µg/m³. Maslahat: %s.\n",
		loc.Name, loc.Country, *f.Current.AQI, level, f.Current.PM25, f.Current.PM10, advice)
	// Kunlar bo'yicha eng yuqori AQI va uning soati (vaqt "YYYY-MM-DDTHH:MM", mahalliy).
	type peak struct {
		day, hour string
		v         float64
	}
	var days []peak
	for i, t := range f.Hourly.Time {
		if i >= len(f.Hourly.AQI) || f.Hourly.AQI[i] == nil || len(t) < 16 {
			continue
		}
		d, h, v := t[:10], t[11:16], *f.Hourly.AQI[i]
		if len(days) == 0 || days[len(days)-1].day != d {
			days = append(days, peak{d, h, v})
		} else if v > days[len(days)-1].v {
			days[len(days)-1] = peak{d, h, v}
		}
	}
	names := []string{"Bugun", "Ertaga"}
	for i, p := range days {
		if i >= len(names) {
			break
		}
		lv, _ := aqiLevel(p.v)
		fmt.Fprintf(&b, "%s (%s): eng yuqori AQI %.0f soat %s atrofida — %s.\n", names[i], p.day, p.v, p.hour, lv)
	}
	return b.String(), nil
}
