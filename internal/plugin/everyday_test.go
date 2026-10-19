package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func geoServer(t *testing.T, extra http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/geo" {
			if r.URL.Query().Get("name") == "Yo'q" {
				w.Write([]byte(`{}`))
				return
			}
			w.Write([]byte(`{"results":[{"name":"Tashkent","country":"Uzbekistan","latitude":41.26,"longitude":69.21}]}`))
			return
		}
		if r.URL.Query().Get("latitude") != "41.26" {
			w.WriteHeader(400)
			return
		}
		extra(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAirQuality(t *testing.T) {
	srv := geoServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("current"), "us_aqi") {
			w.WriteHeader(400)
			return
		}
		w.Write([]byte(`{"current":{"us_aqi":168,"pm2_5":88.4,"pm10":120.2},"hourly":{
			"time":["2026-10-19T00:00","2026-10-19T08:00","2026-10-19T21:00","2026-10-20T07:00","2026-10-20T20:00"],
			"us_aqi":[90,180,null,40,55]}}`))
	})
	a := &Air{GeoURL: srv.URL + "/geo", BaseURL: srv.URL + "/air"}
	out, err := a.Call(context.Background(), Config{"city": "Toshkent"}, "air_quality", nil)
	if err != nil || !strings.Contains(out, "Tashkent (Uzbekistan). Hozir AQI 168 — zararli 🔴. PM2.5: 88 µg/m³, PM10: 120 µg/m³. Maslahat: ochiq havoda sport qilmang") ||
		!strings.Contains(out, "Bugun (2026-10-19): eng yuqori AQI 180 soat 08:00") || !strings.Contains(out, "Ertaga (2026-10-20): eng yuqori AQI 55 soat 20:00 atrofida — o'rtacha") {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := a.Call(context.Background(), nil, "", map[string]any{"city": "Yo'q"}); err == nil || !strings.Contains(err.Error(), "shahar topilmadi") {
		t.Fatalf("shahar: %v", err)
	}
	for v, want := range map[float64]string{30: "yaxshi", 75: "o'rtacha", 120: "sezgir", 250: "juda zararli", 400: "xavfli"} {
		if l, _ := aqiLevel(v); !strings.HasPrefix(l, want) {
			t.Fatalf("%v: %s", v, l)
		}
	}
}

func at(s string) func() time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, uzLoc)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return t }
}

func TestHolidays(t *testing.T) {
	h := &Holidays{Now: at("2026-08-30 10:00")}
	out, _ := h.Call(context.Background(), nil, "", map[string]any{"days": float64(35)})
	for _, want := range []string{
		"Bugun 30.08.2026, yakshanba.",
		"• Qatag'on qurbonlarini yod etish kuni (31.08.2026, dushanba) — ertaga\n",
		"• Mustaqillik kuni (01.09.2026, seshanba) — 2 kundan keyin — dam olish kuni",
		"• O'qituvchi va murabbiylar kuni (01.10.2026, payshanba) — 32 kundan keyin — dam olish kuni",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q yo'q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "O'zbek tili") {
		t.Fatal("oraliqdan tashqari")
	}
	// Ramazon oyi va hayit; yil oxiridan keyingi yilga o'tish.
	out, _ = (&Holidays{Now: at("2026-03-01 09:00")}).Call(context.Background(), nil, "", map[string]any{"days": float64(20)})
	if !strings.Contains(out, "Hozir Ramazon oyi (taxminan 12-kun), Ramazon hayitigacha 19 kun") || !strings.Contains(out, "Ramazon hayiti (taxminiy sana) (20.03.2026, juma) — 19 kundan keyin — dam olish kuni") {
		t.Fatalf("ramazon:\n%s", out)
	}
	out, _ = (&Holidays{Now: at("2026-12-31 09:00")}).Call(context.Background(), nil, "", map[string]any{"days": float64(1)})
	if !strings.Contains(out, "Yangi yil (01.01.2027, juma) — ertaga — dam olish kuni") {
		t.Fatalf("yangi yil:\n%s", out)
	}
	out, _ = (&Holidays{Now: at("2026-07-02 09:00")}).Call(context.Background(), nil, "", map[string]any{"days": float64(5)})
	if !strings.Contains(out, "Keyingi 5 kunda bayram yo'q") {
		t.Fatalf("bo'sh:\n%s", out)
	}
	// Har yil uchun hijriy sanalar tartibli: Ramazon < hayit < Qurbon hayiti.
	for y, d := range islamicDates {
		a, b, c := day(y, time.Month(d[0][0]), d[0][1]), day(y, time.Month(d[1][0]), d[1][1]), day(y, time.Month(d[2][0]), d[2][1])
		if n := b.Sub(a).Hours() / 24; n < 28 || n > 31 || !c.After(b) {
			t.Fatalf("%d: %v %v %v", y, a, b, c)
		}
	}
}

func TestMarkets(t *testing.T) {
	var goldDown bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gold/XAU":
			if goldDown {
				w.WriteHeader(503)
				return
			}
			w.Write([]byte(`{"name":"Gold","price":3110.35,"symbol":"XAU"}`))
		case "/gold/XAG":
			w.Write([]byte(`{"name":"Silver","price":31.1034768,"symbol":"XAG"}`))
		case "/cg/simple/price":
			ids := r.URL.Query().Get("ids")
			if ids == "pax-gold" {
				w.Write([]byte(`{"pax-gold":{"usd":3110.35,"usd_24h_change":0.1}}`))
				return
			}
			if ids != "bitcoin,the-open-network" || r.URL.Query().Get("include_24hr_change") != "true" {
				w.WriteHeader(400)
				return
			}
			w.Write([]byte(`{"bitcoin":{"usd":104250.7,"usd_24h_change":-2.345},"the-open-network":{"usd":3.214,"usd_24h_change":5.01}}`))
		case "/cbu":
			w.Write([]byte(`[{"Ccy":"EUR","Rate":"14000"},{"Ccy":"USD","Rate":"12650.00"}]`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	m := &Markets{GoldURL: srv.URL + "/gold", CoinURL: srv.URL + "/cg", CBUURL: srv.URL + "/cbu"}
	ctx := context.Background()

	out, err := m.Call(ctx, Config{"coins": "BTC, ton"}, "crypto_price", nil)
	if err != nil || !strings.Contains(out, "BTC: $104 251 (24 soatda -2.3%)") || !strings.Contains(out, "TON: $3.21 (24 soatda +5.0%)") {
		t.Fatalf("kripto: %q %v", out, err)
	}
	if _, err := m.Call(ctx, nil, "crypto_price", map[string]any{"coins": "BTC,SCAM"}); err == nil || !strings.Contains(err.Error(), "SCAM") {
		t.Fatalf("noma'lum: %v", err)
	}
	if err := m.Validate(Config{"coins": "btc eth"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(Config{"coins": "xyz"}); err == nil {
		t.Fatal("noto'g'ri valyuta saqlandi")
	}

	out, err = m.Call(ctx, nil, "gold_price", nil)
	// 3110.35 / 31.1034768 = 100.0001 $/g → 1 265 001 so'm; kumush 1$/g → 12 650 so'm.
	if err != nil || !strings.Contains(out, "Oltin (999): 1 troy unsiya $3 110, 1 gramm $100.00 ≈ 1 265 001 so'm") ||
		!strings.Contains(out, "Kumush (999): 1 troy unsiya $31.10, 1 gramm $1.00 ≈ 12 650 so'm") || !strings.Contains(out, "1$ = 12 650 so'm") {
		t.Fatalf("oltin: %q %v", out, err)
	}
	goldDown = true // zaxira: PAXG
	if out, err := m.Call(ctx, nil, "gold_price", nil); err != nil || !strings.Contains(out, "1 gramm $100.00") {
		t.Fatalf("zaxira: %q %v", out, err)
	}
	if groupThousands("1234567") != "1 234 567" || groupThousands("999") != "999" || money(0.00012) != "0.000120" {
		t.Fatal("format")
	}
}

func TestDaily(t *testing.T) {
	d := &Daily{Now: at("2026-10-19 08:00")}
	out, _ := d.Call(context.Background(), Config{"kind": "both"}, "", nil)
	if !strings.HasPrefix(out, "📘 Kun so'zi:\n") || !strings.Contains(out, "Misol: ") || !strings.Contains(out, "🪶 Kun maqoli: ") {
		t.Fatalf("%q", out)
	}
	same, _ := (&Daily{Now: at("2026-10-19 23:59")}).Call(context.Background(), Config{"kind": "both"}, "", nil)
	next, _ := (&Daily{Now: at("2026-10-20 00:01")}).Call(context.Background(), Config{"kind": "both"}, "", nil)
	if same != out || next == out {
		t.Fatal("kun ichida bir xil, ertasiga boshqa bo'lishi kerak")
	}
	if out, _ := d.Call(context.Background(), Config{"kind": "proverb"}, "", nil); strings.Contains(out, "Kun so'zi") || !strings.Contains(out, "maqoli") {
		t.Fatalf("faqat maqol: %q", out)
	}
	if out, _ := d.Call(context.Background(), nil, "", map[string]any{"kind": "word", "count": float64(3)}); strings.Count(out, "Misol:") != 3 || strings.Contains(out, "maqol") {
		t.Fatalf("3 so'z: %q", out)
	}
	// Ro'yxat tugaguncha takrorlanmaydi.
	seen := map[string]bool{}
	start := at("2026-01-01 12:00")()
	for i := 0; i < len(dailyWords); i++ {
		w := dailyWords[dayIndex(start.AddDate(0, 0, i))%len(dailyWords)].en
		if seen[w] {
			t.Fatalf("takror: %s", w)
		}
		seen[w] = true
	}
	for _, w := range dailyWords {
		if w.en == "" || w.ipa == "" || w.uz == "" || w.example == "" {
			t.Fatalf("to'liq emas: %+v", w)
		}
	}
}

func TestSunMoon(t *testing.T) {
	srv := geoServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("daily") != "sunrise,sunset,daylight_duration" {
			w.WriteHeader(400)
			return
		}
		w.Write([]byte(`{"daily":{"time":["2026-10-19"],"sunrise":["2026-10-19T06:47"],"sunset":["2026-10-19T17:40"],"daylight_duration":[39180]}}`))
	})
	s := &Sky{GeoURL: srv.URL + "/geo", BaseURL: srv.URL + "/f", Now: at("2026-10-19 08:00")}
	out, err := s.Call(context.Background(), nil, "", nil)
	if err != nil || !strings.Contains(out, "2026-10-19: quyosh chiqishi 06:47, botishi 17:40, kun uzunligi 10 soat 53 daqiqa") || !strings.Contains(out, "Oy: ") {
		t.Fatalf("%q %v", out, err)
	}
	// Ma'lum fazalar (±1 kun aniqlik): 2025-01-13 to'lin oy, 2025-01-29 yangi oy.
	if p, lit := moonPhase(time.Date(2025, 1, 13, 22, 0, 0, 0, time.UTC)); !strings.Contains(p, "to'lin") || lit < 95 {
		t.Fatalf("to'lin: %s %.0f", p, lit)
	}
	if p, lit := moonPhase(time.Date(2025, 1, 29, 12, 0, 0, 0, time.UTC)); !strings.Contains(p, "yangi") || lit > 5 {
		t.Fatalf("yangi: %s %.0f", p, lit)
	}
	if n := nextPhase(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), 0.5); n.Before(time.Date(2025, 1, 12, 0, 0, 0, 0, time.UTC)) || n.After(time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("keyingi to'lin oy: %v", n)
	}
}

func TestCountdown(t *testing.T) {
	c := &Countdown{Now: at("2026-10-19 08:00")}
	cfg := Config{
		"event1": "To'y, 15.11.2026",
		"event2": "Onamning tug'ilgan kuni, 18.10", // har yili: bu yilgisi kecha o'tdi → keyingi yil
		"event3": "Imtihon, 20.10.2026",
		"event4": "Eski loyiha, 09.10.2026",
		"event5": "Bugungi uchrashuv, 19.10.2026",
	}
	if err := c.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	out, _ := c.Call(context.Background(), cfg, "", nil)
	want := "Bugun 19.10.2026, dushanba.\n" +
		"🎉 Bugungi uchrashuv — BUGUN (19.10.2026, dushanba)\n" +
		"⏳ Imtihon — ertaga (20.10.2026, seshanba)\n" +
		"⏳ To'y — 27 kun qoldi (15.11.2026, yakshanba)\n" +
		"⏳ Onamning tug'ilgan kuni — 364 kun qoldi (18.10.2027, dushanba) (har yili)\n" +
		"✔️ Eski loyiha — 10 kun oldin o'tdi (09.10.2026, juma)\n"
	if out != want {
		t.Fatalf("\n%s\nkutilgan:\n%s", out, want)
	}
	for _, bad := range []string{"To'y 15.11.2026", "To'y, 31.02.2026", ", 01.01.2027", "To'y, ertaga"} {
		if err := c.Validate(Config{"event1": bad}); err == nil {
			t.Fatalf("qabul qilindi: %q", bad)
		}
	}
	if _, err := Clean(c.Info(), Config{}, nil); err == nil {
		t.Fatal("birinchi sana majburiy")
	}
}

func TestEverydayInRegistry(t *testing.T) {
	r := Default(Options{})
	for _, id := range []string{"air", "holidays", "markets", "daily", "sky", "countdown"} {
		p, ok := r.Get(id)
		if !ok || p.Info().Soon || len(p.Tools()) == 0 {
			t.Fatalf("%s reestrda yo'q yoki ishlamaydi", id)
		}
	}
}
