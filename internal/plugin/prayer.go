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

// prayerRegions — islomapi.uz hududlari va aladhan.com uchun inglizcha shahar nomi.
var prayerRegions = []struct{ Name, City string }{
	{"Toshkent", "Tashkent"}, {"Andijon", "Andijan"}, {"Buxoro", "Bukhara"}, {"Guliston", "Gulistan"},
	{"Jizzax", "Jizzakh"}, {"Qarshi", "Karshi"}, {"Navoiy", "Navoiy"}, {"Namangan", "Namangan"},
	{"Nukus", "Nukus"}, {"Samarqand", "Samarkand"}, {"Termiz", "Termez"}, {"Urganch", "Urgench"},
	{"Farg'ona", "Fergana"}, {"Xiva", "Khiva"}, {"Qo'qon", "Kokand"}, {"Marg'ilon", "Margilan"},
}

func regionOptions() []Option {
	out := make([]Option, 0, len(prayerRegions))
	for _, r := range prayerRegions {
		out = append(out, Option{r.Name, r.Name})
	}
	return out
}

// Prayer — namoz vaqtlari: asosiy manba islomapi.uz (O'zbekiston musulmonlari idorasi
// taqvimi asosida), ishlamasa aladhan.com (Hanafiy, asr — ikki soya).
type Prayer struct {
	HTTP                 *http.Client
	IslomURL, AladhanURL string
}

func (p *Prayer) Info() Info {
	return Info{ID: "prayer", Name: "Namoz vaqtlari", Icon: "🕌", Category: "Kundalik",
		Description: "Shahringiz bo'yicha bugungi (yoki ertangi) namoz vaqtlari: bomdod, quyosh, peshin, asr, shom, xufton.",
		Fields:      []Field{{Key: "region", Label: "Shahar", Type: FieldSelect, Options: regionOptions(), Default: "Toshkent"}},
		Examples:    []string{"Har kuni 04:30 da bugungi namoz vaqtlari", "Ramazonda har kuni saharlik va iftorlik vaqti"}}
}

func (p *Prayer) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{{Name: "prayer_times", Description: "Namoz vaqtlari (O'zbekiston shaharlari). Shahar berilmasa — egasining shahri.",
		Params: obj(map[string]any{
			"region": map[string]any{"type": "STRING", "description": "Shahar, masalan Toshkent, Samarqand (ixtiyoriy)"},
			"day":    map[string]any{"type": "STRING", "enum": []string{"today", "tomorrow"}, "description": "Bugun (standart) yoki ertaga"},
		})}}
}

// prayerOrder — chiqish tartibi va o'zbekcha nomlar (ikkala manba kalitlari).
var prayerOrder = []struct {
	Label string
	Keys  []string
}{
	{"Bomdod (saharlik)", []string{"tong_saharlik", "Fajr"}},
	{"Quyosh", []string{"quyosh", "Sunrise"}},
	{"Peshin", []string{"peshin", "Dhuhr"}},
	{"Asr", []string{"asr", "Asr"}},
	{"Shom (iftorlik)", []string{"shom_iftor", "Maghrib"}},
	{"Xufton", []string{"hufton", "xufton", "Isha"}},
}

func findRegion(v string) (name, city string, ok bool) {
	v = strings.ToLower(strings.NewReplacer("ʻ", "'", "ʼ", "'", "‘", "'", "’", "'").Replace(strings.TrimSpace(v)))
	for _, r := range prayerRegions {
		if strings.ToLower(r.Name) == v || strings.ToLower(r.City) == v {
			return r.Name, r.City, true
		}
	}
	return "", "", false
}

func (p *Prayer) Call(ctx context.Context, cfg Config, _ string, args map[string]any) (string, error) {
	region, city, ok := findRegion(argString(args, "region"))
	if !ok {
		region, city, ok = findRegion(cfg["region"])
	}
	if !ok {
		region, city = "Toshkent", "Tashkent"
	}
	day := time.Now().In(uzLoc)
	label := "Bugun"
	if argString(args, "day") == "tomorrow" {
		day, label = day.AddDate(0, 0, 1), "Ertaga"
	}
	times, extra, err := p.islom(ctx, region, day)
	if err != nil {
		times, extra, err = p.aladhan(ctx, city, day)
	}
	if err != nil {
		return "", errors.New("namoz vaqtlarini olib bo'lmadi, keyinroq urinib ko'ring")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s, %s — %s namoz vaqtlari%s:\n", label, day.Format("2006-01-02"), region, extra)
	for _, o := range prayerOrder {
		for _, k := range o.Keys {
			if t := times[k]; t != "" {
				fmt.Fprintf(&b, "• %s — %s\n", o.Label, t)
				break
			}
		}
	}
	return b.String(), nil
}

var uzLoc = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Tashkent"); err == nil {
		return l
	}
	return time.FixedZone("UZT", 5*3600)
}()

func (p *Prayer) islom(ctx context.Context, region string, day time.Time) (map[string]string, string, error) {
	base := p.IslomURL
	if base == "" {
		base = "https://islomapi.uz/api"
	}
	path := "/present/day"
	q := url.Values{"region": {region}}
	if day.YearDay() != time.Now().In(uzLoc).YearDay() {
		path = "/daily"
		q.Set("month", fmt.Sprint(int(day.Month())))
		q.Set("day", fmt.Sprint(day.Day()))
	}
	data, err := getBody(ctx, p.HTTP, base+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	var out struct {
		Times map[string]string `json:"times"`
		Hijri struct {
			Month string `json:"month"`
			Day   any    `json:"day"`
		} `json:"hijri_date"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Times) == 0 {
		return nil, "", errors.New("islomapi javobi noto'g'ri")
	}
	extra := ""
	if out.Hijri.Month != "" {
		extra = fmt.Sprintf(" (hijriy: %v %s)", out.Hijri.Day, out.Hijri.Month)
	}
	return out.Times, extra, nil
}

func (p *Prayer) aladhan(ctx context.Context, city string, day time.Time) (map[string]string, string, error) {
	base := p.AladhanURL
	if base == "" {
		base = "https://api.aladhan.com/v1"
	}
	q := url.Values{"city": {city}, "country": {"Uzbekistan"}, "method": {"3"}, "school": {"1"}}
	data, err := getBody(ctx, p.HTTP, base+"/timingsByCity/"+day.Format("02-01-2006")+"?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	var out struct {
		Data struct {
			Timings map[string]string `json:"timings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Data.Timings) == 0 {
		return nil, "", errors.New("aladhan javobi noto'g'ri")
	}
	for k, v := range out.Data.Timings { // "05:12 (+05)" → "05:12"
		if i := strings.IndexByte(v, ' '); i > 0 {
			out.Data.Timings[k] = v[:i]
		}
	}
	return out.Data.Timings, " (taxminiy hisob)", nil
}
