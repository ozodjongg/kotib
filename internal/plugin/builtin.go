package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// Options — standart plaginlar uchun server sozlamalari.
type Options struct {
	Search llm.Searcher // nil → internet qidiruvi plagini yo'q
	// YouTubeToken — YouTube akkaunti (Google OAuth) refresh tokenidan access token oladi
	// (google.Client.PluginAccessToken). nil → Google sozlanmagan: YouTube «tez orada».
	YouTubeToken func(ctx context.Context, refreshToken string) (string, error)
	// SheetsToken — Google Sheets akkaunti uchun (xuddi shunday); nil → «tez orada».
	SheetsToken func(ctx context.Context, refreshToken string) (string, error)
	// DriveToken — Google Drive akkaunti uchun (xuddi shunday); nil → «tez orada».
	DriveToken func(ctx context.Context, refreshToken string) (string, error)
	// TelegramFeed — egasining kanal/guruh xabarlari (userbot.Manager.Feed); nil → plagin yo'q.
	TelegramFeed TelegramFeed
	HTTP         *http.Client
	// Testlar uchun manzillar (bo'sh — haqiqiy xizmatlar).
	YouTubeURL, GeoURL, WeatherURL, CBUURL, SheetsURL, DriveURL, NotionURL, SportURL string
}

// Default — standart plaginlar reestri.
func Default(o Options) *Registry {
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 20 * time.Second}
	}
	var ps []Plugin
	if o.Search != nil {
		ps = append(ps, &WebSearch{S: o.Search})
	}
	if o.TelegramFeed != nil {
		ps = append(ps, &Telegram{Feed: o.TelegramFeed})
	}
	ps = append(ps,
		youtubePlugin(o),
		&Prayer{HTTP: o.HTTP},
		&News{HTTP: o.HTTP},
		sheetsPlugin(o),
		drivePlugin(o),
		&Notion{HTTP: o.HTTP, BaseURL: o.NotionURL},
		&Sport{HTTP: o.HTTP, BaseURL: o.SportURL},
		&AmoCRM{HTTP: o.HTTP},
		&Bitrix24{HTTP: o.HTTP},
		&Weather{HTTP: o.HTTP, GeoURL: o.GeoURL, BaseURL: o.WeatherURL},
		&Currency{HTTP: o.HTTP, BaseURL: o.CBUURL},
		soon("gmail", "Gmail", "📧", "Pochta", "Kiruvchi xatlarni o'qish, muhimlarini xulosa qilish."),
		soon("instagram", "Instagram", "📸", "Ijtimoiy tarmoq", "Direct xabarlari va izohlarga javob qoralamalari."),
	)
	r, err := NewRegistry(ps...)
	if err != nil {
		panic(err) // standart to'plam — dasturchi xatosi
	}
	return r
}

// ---- Tez orada ----

type comingSoon struct{ info Info }

func soon(id, name, icon, cat, desc string) Plugin {
	return &comingSoon{Info{ID: id, Name: name, Icon: icon, Category: cat, Description: desc, Soon: true}}
}

func (c *comingSoon) Info() Info            { return c.info }
func (c *comingSoon) Tools() []llm.ToolDecl { return nil }
func (c *comingSoon) Call(context.Context, Config, string, map[string]any) (string, error) {
	return "", errors.New("plagin hali mavjud emas")
}

// ---- Internet qidiruvi ----

type WebSearch struct{ S llm.Searcher }

func (w *WebSearch) Info() Info {
	return Info{ID: "web", Name: "Internet qidiruvi", Icon: "🌐", Category: "Qidiruv",
		Description: "Google qidiruvi orqali yangiliklar, narxlar, sport natijalari va boshqa dolzarb ma'lumot.",
		Examples:    []string{"Har kuni 9:00 da texnologiya yangiliklarining qisqa xulosasi", "Juma kuni — dam olish kunlari Toshkentdagi tadbirlar"}}
}

func (w *WebSearch) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{{Name: "web_search", Description: "Internetdan (Google) qidiradi va topilgan ma'lumotni manbalari bilan qaytaradi.",
		Params: obj(map[string]any{"query": str("Qidiruv so'rovi")}, "query")}}
}

func (w *WebSearch) Call(ctx context.Context, _ Config, _ string, args map[string]any) (string, error) {
	q := argString(args, "query")
	if q == "" {
		return "", errors.New("query kerak")
	}
	return w.S.WebSearch(ctx, q)
}

// ---- Ob-havo (Open-Meteo, kalitsiz) ----

type Weather struct {
	HTTP            *http.Client
	GeoURL, BaseURL string
}

func (w *Weather) Info() Info {
	return Info{ID: "weather", Name: "Ob-havo", Icon: "🌤", Category: "Kundalik",
		Description: "Istalgan shahar uchun hozirgi ob-havo va 7 kunlik prognoz (Open-Meteo, kalit shart emas).",
		Fields:      []Field{{Key: "city", Label: "Asosiy shahar", Type: FieldText, Default: "Toshkent", Placeholder: "Toshkent"}},
		Examples:    []string{"Har kuni 7:30 da bugungi ob-havo va nima kiyish haqida maslahat"}}
}

func (w *Weather) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{{Name: "weather", Description: "Shahar ob-havosi: hozirgi harorat va kunlik prognoz. Shahar berilmasa — egasining asosiy shahri.",
		Params: obj(map[string]any{
			"city": str("Shahar nomi (ixtiyoriy)"),
			"days": map[string]any{"type": "INTEGER", "description": "Prognoz kunlari 1–7, standart 1"},
		})}}
}

var wmo = map[int]string{0: "ochiq", 1: "asosan ochiq", 2: "qisman bulutli", 3: "bulutli", 45: "tuman", 48: "qirov tuman",
	51: "yengil shivalash", 53: "shivalash", 55: "kuchli shivalash", 61: "yengil yomg'ir", 63: "yomg'ir", 65: "kuchli yomg'ir",
	66: "muzli yomg'ir", 67: "kuchli muzli yomg'ir", 71: "yengil qor", 73: "qor", 75: "kuchli qor", 77: "qor donalari",
	80: "jala", 81: "kuchli jala", 82: "juda kuchli jala", 85: "qor yog'ishi", 86: "kuchli qor yog'ishi",
	95: "momaqaldiroq", 96: "do'l bilan momaqaldiroq", 99: "kuchli do'l bilan momaqaldiroq"}

func (w *Weather) Call(ctx context.Context, cfg Config, _ string, args map[string]any) (string, error) {
	city := argString(args, "city")
	if city == "" {
		city = cfg["city"]
	}
	if city == "" {
		city = "Toshkent"
	}
	days := argInt(args, "days", 1, 1, 7)
	geo := w.GeoURL
	if geo == "" {
		geo = "https://geocoding-api.open-meteo.com/v1/search"
	}
	data, err := getBody(ctx, w.HTTP, geo+"?"+url.Values{"name": {city}, "count": {"1"}, "language": {"en"}}.Encode(), nil)
	if err != nil {
		return "", err
	}
	var g struct {
		Results []struct {
			Name      string  `json:"name"`
			Country   string  `json:"country"`
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"results"`
	}
	if json.Unmarshal(data, &g) != nil || len(g.Results) == 0 {
		return "", fmt.Errorf("shahar topilmadi: %s", city)
	}
	loc := g.Results[0]
	base := w.BaseURL
	if base == "" {
		base = "https://api.open-meteo.com/v1/forecast"
	}
	q := url.Values{
		"latitude": {fmt.Sprint(loc.Latitude)}, "longitude": {fmt.Sprint(loc.Longitude)}, "timezone": {"auto"},
		"current":       {"temperature_2m,apparent_temperature,weather_code,wind_speed_10m"},
		"daily":         {"weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max"},
		"forecast_days": {fmt.Sprint(days)},
	}
	if data, err = getBody(ctx, w.HTTP, base+"?"+q.Encode(), nil); err != nil {
		return "", err
	}
	var f struct {
		Current struct {
			Temp  float64 `json:"temperature_2m"`
			Feels float64 `json:"apparent_temperature"`
			Code  int     `json:"weather_code"`
			Wind  float64 `json:"wind_speed_10m"`
		} `json:"current"`
		Daily struct {
			Time []string  `json:"time"`
			Code []int     `json:"weather_code"`
			Max  []float64 `json:"temperature_2m_max"`
			Min  []float64 `json:"temperature_2m_min"`
			Rain []float64 `json:"precipitation_probability_max"`
		} `json:"daily"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return "", errors.New("ob-havo javobini o'qib bo'lmadi")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s). Hozir: %+.0f°C (his qilinadi %+.0f°C), %s, shamol %.0f km/soat.\n",
		loc.Name, loc.Country, f.Current.Temp, f.Current.Feels, wmo[f.Current.Code], f.Current.Wind)
	for i, d := range f.Daily.Time {
		if i >= len(f.Daily.Max) || i >= len(f.Daily.Min) || i >= len(f.Daily.Code) {
			break
		}
		rain := ""
		if i < len(f.Daily.Rain) {
			rain = fmt.Sprintf(", yog'ingarchilik ehtimoli %.0f%%", f.Daily.Rain[i])
		}
		fmt.Fprintf(&b, "%s: %+.0f…%+.0f°C, %s%s\n", d, f.Daily.Min[i], f.Daily.Max[i], wmo[f.Daily.Code[i]], rain)
	}
	return b.String(), nil
}

// ---- Valyuta kursi (O'zbekiston Markaziy banki) ----

type Currency struct {
	HTTP    *http.Client
	BaseURL string // default https://cbu.uz/uz/arkhiv-kursov-valyut/json/
}

func (c *Currency) Info() Info {
	return Info{ID: "currency", Name: "Valyuta kursi", Icon: "💱", Category: "Moliya",
		Description: "O'zbekiston Markaziy bankining rasmiy kurslari (dollar, yevro, rubl va boshqalar).",
		Fields:      []Field{{Key: "codes", Label: "Kuzatiladigan valyutalar", Type: FieldText, Default: "USD,EUR,RUB", Placeholder: "USD,EUR,RUB"}},
		Examples:    []string{"Har ish kuni 10:00 da dollar va yevro kursi, kechagiga nisbatan o'zgarishi bilan"}}
}

func (c *Currency) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{{Name: "exchange_rates", Description: "Markaziy bankning bugungi valyuta kurslari (so'mda) va kunlik o'zgarishi.",
		Params: obj(map[string]any{"codes": str("Valyuta kodlari vergul bilan, masalan USD,EUR (ixtiyoriy)")})}}
}

func (c *Currency) Call(ctx context.Context, cfg Config, _ string, args map[string]any) (string, error) {
	codes := argString(args, "codes")
	if codes == "" {
		codes = cfg["codes"]
	}
	if codes == "" {
		codes = "USD,EUR,RUB"
	}
	want := map[string]bool{}
	for _, s := range strings.Split(strings.ToUpper(codes), ",") {
		if s = strings.TrimSpace(s); s != "" {
			want[s] = true
		}
	}
	base := c.BaseURL
	if base == "" {
		base = "https://cbu.uz/uz/arkhiv-kursov-valyut/json/"
	}
	data, err := getBody(ctx, c.HTTP, base, nil)
	if err != nil {
		return "", err
	}
	var rows []struct {
		Ccy     string `json:"Ccy"`
		Name    string `json:"CcyNm_UZ"`
		Nominal string `json:"Nominal"`
		Rate    string `json:"Rate"`
		Diff    string `json:"Diff"`
		Date    string `json:"Date"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		return "", errors.New("kurs javobini o'qib bo'lmadi")
	}
	var b strings.Builder
	for _, r := range rows {
		if !want[r.Ccy] {
			continue
		}
		if b.Len() == 0 {
			fmt.Fprintf(&b, "Markaziy bank kurslari (%s):\n", r.Date)
		}
		nom := ""
		if r.Nominal != "" && r.Nominal != "1" {
			nom = r.Nominal + " "
		}
		fmt.Fprintf(&b, "%s%s (%s): %s so'm (o'zgarish %s)\n", nom, r.Ccy, r.Name, r.Rate, r.Diff)
	}
	if b.Len() == 0 {
		return "So'ralgan valyutalar topilmadi.", nil
	}
	return b.String(), nil
}

// ---- sxema yordamchilari ----

func str(desc string) map[string]any { return map[string]any{"type": "STRING", "description": desc} }

func obj(props map[string]any, required ...string) map[string]any {
	o := map[string]any{"type": "OBJECT", "properties": props}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}
