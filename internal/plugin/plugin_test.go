package plugin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeSearch struct{ q string }

func (f *fakeSearch) WebSearch(_ context.Context, q string) (string, error) {
	f.q = q
	return "natija: " + q, nil
}

func TestCatalogAndToolset(t *testing.T) {
	s := &fakeSearch{}
	r := Default(Options{Search: s})
	cat := r.Catalog()
	if len(cat) < 5 || cat[0].ID != "web" || !cat[len(cat)-1].Soon {
		t.Fatalf("katalog: %+v", cat)
	}
	for i := 1; i < len(cat); i++ {
		if cat[i-1].Soon && !cat[i].Soon {
			t.Fatal("tez oradagilar oxirida bo'lishi kerak")
		}
	}
	decls, call := r.Toolset([]Enabled{{ID: "web"}, {ID: "gmail"}, {ID: "yo'q"}})
	if len(decls) != 1 || decls[0].Name != "web_search" {
		t.Fatalf("vositalar: %+v", decls)
	}
	out, err := call(context.Background(), "web_search", map[string]any{"query": "yangiliklar"})
	if err != nil || out != "natija: yangiliklar" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := call(context.Background(), "weather", nil); err == nil {
		t.Fatal("yoqilmagan plagin vositasi chaqirilmasligi kerak")
	}
	if _, err := call(context.Background(), "rm_rf", nil); err == nil {
		t.Fatal("noma'lum vosita")
	}
	if _, err := NewRegistry(&Weather{}, &Weather{}); err == nil {
		t.Fatal("takror ID rad etilishi kerak")
	}
	if r2 := Default(Options{}); func() bool { _, ok := r2.Get("web"); return ok }() {
		t.Fatal("Searcher'siz internet qidiruvi bo'lmasligi kerak")
	}
}

func TestCleanAndPublic(t *testing.T) {
	cur := (&Currency{}).Info()
	cfg, err := Clean(cur, Config{"codes": " USD,EUR ", "evil": "x"}, nil)
	if err != nil || cfg["codes"] != "USD,EUR" || cfg["evil"] != "" {
		t.Fatalf("%v %v", cfg, err)
	}
	if cfg, _ := Clean((&Weather{}).Info(), Config{}, nil); cfg["city"] != "Toshkent" {
		t.Fatalf("standart qiymat: %v", cfg)
	}
	if _, err := Clean(cur, Config{"codes": "a\nb"}, nil); err == nil {
		t.Fatal("ko'p qatorli qiymat")
	}
	sel := Info{Fields: []Field{{Key: "k", Label: "K", Type: FieldSelect, Options: []Option{{"a", "A"}}},
		{Key: "s", Label: "S", Type: FieldSecret}}}
	if _, err := Clean(sel, Config{"k": "b"}, nil); err == nil {
		t.Fatal("ro'yxatda yo'q select")
	}
	c2, _ := Clean(sel, Config{"k": "a"}, Config{"s": "OLDSECRET1234"})
	if c2["s"] != "OLDSECRET1234" {
		t.Fatal("bo'sh secret — eskisi saqlanadi")
	}
	if pub := Public(sel, c2); pub["s"] != "••••1234" || pub["k"] != "a" {
		t.Fatalf("public: %v", pub)
	}
	if _, err := Clean(soon("x", "X", "", "", "").Info(), Config{}, nil); err == nil {
		t.Fatal("tez orada plagin yoqilmasligi kerak")
	}
	// Akkaunt orqali ulanadigan plagin: Mini App'dan kelgan qiymatlar hisobga olinmaydi.
	yt := (&YouTube{}).Info()
	if _, err := Clean(yt, Config{"refresh_token": "hack"}, nil); err == nil {
		t.Fatal("ulanmagan akkaunt")
	}
	cfg, err = Clean(yt, Config{"refresh_token": "hack", "account": "x"}, Config{KeyRefreshToken: "RT", KeyAccount: "a@x.uz"})
	if err != nil || cfg[KeyRefreshToken] != "RT" || cfg[KeyAccount] != "a@x.uz" {
		t.Fatalf("connect clean: %v %v", cfg, err)
	}
	if len(Public(yt, cfg)) != 0 {
		t.Fatal("token Mini App'ga chiqmasligi kerak")
	}
}

func TestYouTube(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer AT" {
			w.WriteHeader(401)
			return
		}
		queries = append(queries, r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Path {
		case "/search":
			w.Write([]byte(`{"items":[{"id":{"videoId":"abc123"},"snippet":{"title":"Go &#39;dars&#39;","channelTitle":"Kanal","publishedAt":"2026-10-15T10:00:00Z"}}]}`))
		case "/subscriptions":
			w.Write([]byte(`{"items":[{"snippet":{"title":"Kun.uz","resourceId":{"channelId":"UCaaa"}}},{"snippet":{"title":"Eski","resourceId":{"channelId":"UCbbb"}}}]}`))
		case "/playlistItems":
			now := time.Now().UTC()
			if r.URL.Query().Get("playlistId") == "UUaaa" {
				fmt.Fprintf(w, `{"items":[{"snippet":{"title":"Bugungi yangilik","publishedAt":%q},"contentDetails":{"videoId":"new1"}},
					{"snippet":{"title":"Eski video","publishedAt":%q},"contentDetails":{"videoId":"old1"}}]}`,
					now.Add(-2*time.Hour).Format(time.RFC3339), now.AddDate(0, 0, -5).Format(time.RFC3339))
				return
			}
			w.WriteHeader(404) // bitta kanal xatosi natijani buzmaydi
		}
	}))
	defer srv.Close()
	var gotRT string
	y := &YouTube{BaseURL: srv.URL, Token: func(_ context.Context, rt string) (string, error) {
		gotRT = rt
		if rt == "revoked" {
			return "", errors.New("invalid_grant")
		}
		return "AT", nil
	}}
	cfg := Config{KeyRefreshToken: "RT", KeyAccount: "a@x.uz"}
	out, err := y.Call(context.Background(), cfg, "youtube_search", map[string]any{"query": "go dars", "max_results": float64(50), "order": "date"})
	if err != nil || gotRT != "RT" || !strings.Contains(out, "Go 'dars' — Kanal (2026-10-15)") || !strings.Contains(out, "https://youtu.be/abc123") {
		t.Fatalf("%q %v", out, err)
	}
	for _, want := range []string{"maxResults=10", "order=date", "q=go+dars", "type=video"} {
		if !strings.Contains(queries[0], want) {
			t.Fatalf("%s yo'q: %s", want, queries[0])
		}
	}
	if strings.Contains(queries[0], "key=") {
		t.Fatal("API kalit ishlatilmasligi kerak")
	}
	out, err = y.Call(context.Background(), cfg, "youtube_subscriptions", map[string]any{"days": float64(1)})
	if err != nil || !strings.Contains(out, "Bugungi yangilik — Kun.uz") || strings.Contains(out, "Eski video") || !strings.Contains(out, "youtu.be/new1") {
		t.Fatalf("obunalar: %q %v", out, err)
	}
	if _, err := y.Call(context.Background(), Config{}, "youtube_search", map[string]any{"query": "x"}); err == nil || !strings.Contains(err.Error(), "ulanmagan") {
		t.Fatalf("ulanmagan: %v", err)
	}
	if _, err := y.Call(context.Background(), Config{KeyRefreshToken: "revoked"}, "youtube_search", map[string]any{"query": "x"}); !errors.Is(err, ErrAccountRevoked) {
		t.Fatalf("bekor qilingan: %v", err)
	}
	if p := youtubePlugin(Options{}); !p.Info().Soon {
		t.Fatal("Google sozlanmagan bo'lsa YouTube «tez orada»")
	}
}

func TestWeatherAndCurrency(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/geo", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") == "Yo'qshahar" {
			w.Write([]byte(`{}`))
			return
		}
		w.Write([]byte(`{"results":[{"name":"Tashkent","country":"Uzbekistan","latitude":41.26,"longitude":69.21}]}`))
	})
	mux.HandleFunc("/fc", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("forecast_days") != "2" {
			t.Errorf("days: %s", r.URL.RawQuery)
		}
		w.Write([]byte(`{"current":{"temperature_2m":18.4,"apparent_temperature":17,"weather_code":2,"wind_speed_10m":9},
			"daily":{"time":["2026-10-16","2026-10-17"],"weather_code":[2,61],"temperature_2m_max":[21,19],"temperature_2m_min":[9,10],"precipitation_probability_max":[5,70]}}`))
	})
	mux.HandleFunc("/cbu", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"Ccy":"USD","CcyNm_UZ":"AQSH dollari","Nominal":"1","Rate":"12650.10","Diff":"-12.5","Date":"16.10.2026"},
			{"Ccy":"EUR","CcyNm_UZ":"Yevro","Nominal":"1","Rate":"13700","Diff":"3","Date":"16.10.2026"},
			{"Ccy":"JPY","CcyNm_UZ":"Yen","Nominal":"10","Rate":"850","Diff":"1","Date":"16.10.2026"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	w := &Weather{GeoURL: srv.URL + "/geo", BaseURL: srv.URL + "/fc"}
	out, err := w.Call(context.Background(), Config{"city": "Toshkent"}, "weather", map[string]any{"days": float64(2)})
	if err != nil || !strings.Contains(out, "Tashkent (Uzbekistan). Hozir: +18°C") || !strings.Contains(out, "2026-10-17: +10…+19°C, yengil yomg'ir, yog'ingarchilik ehtimoli 70%") {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := w.Call(context.Background(), Config{}, "weather", map[string]any{"city": "Yo'qshahar"}); err == nil {
		t.Fatal("topilmagan shahar")
	}
	c := &Currency{BaseURL: srv.URL + "/cbu"}
	out, err = c.Call(context.Background(), Config{"codes": "USD,JPY"}, "", nil)
	if err != nil || !strings.Contains(out, "USD (AQSH dollari): 12650.10 so'm (o'zgarish -12.5)") || !strings.Contains(out, "10 JPY") || strings.Contains(out, "EUR") {
		t.Fatalf("%q %v", out, err)
	}
	out, _ = c.Call(context.Background(), Config{}, "", map[string]any{"codes": "eur"})
	if !strings.Contains(out, "EUR") || strings.Contains(out, "USD") {
		t.Fatalf("args kodlari: %q", out)
	}
}
