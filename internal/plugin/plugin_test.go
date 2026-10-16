package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	yt := (&YouTube{}).Info()
	if _, err := Clean(yt, Config{}, nil); err == nil {
		t.Fatal("server kaliti yo'q: kalit majburiy")
	}
	cfg, err := Clean(yt, Config{"api_key": " AIzaSECRET1234 ", "region": "UZ", "evil": "x"}, nil)
	if err != nil || cfg["api_key"] != "AIzaSECRET1234" || cfg["order"] != "relevance" || cfg["evil"] != "" {
		t.Fatalf("%v %v", cfg, err)
	}
	// Secret bo'sh yuborilsa — eskisi saqlanadi.
	cfg2, err := Clean(yt, Config{"region": "RU"}, cfg)
	if err != nil || cfg2["api_key"] != "AIzaSECRET1234" || cfg2["region"] != "RU" {
		t.Fatalf("%v %v", cfg2, err)
	}
	if _, err := Clean(yt, Config{"api_key": "k", "region": "XX"}, nil); err == nil {
		t.Fatal("ro'yxatda yo'q select")
	}
	if _, err := Clean(yt, Config{"api_key": "a\nb"}, nil); err == nil {
		t.Fatal("ko'p qatorli qiymat")
	}
	pub := Public(yt, cfg)
	if pub["api_key"] != "••••1234" || strings.Contains(pub["api_key"], "SECRET") || pub["region"] != "UZ" {
		t.Fatalf("public: %v", pub)
	}
	if _, err := Clean((&YouTube{ServerKey: "srv"}).Info(), Config{}, nil); err != nil {
		t.Fatalf("server kaliti bor: kalit ixtiyoriy: %v", err)
	}
	if _, err := Clean(soon("x", "X", "", "", "").Info(), Config{}, nil); err == nil {
		t.Fatal("tez orada plagin yoqilmasligi kerak")
	}
}

func TestYouTube(t *testing.T) {
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.RawQuery
		if r.URL.Query().Get("key") == "bad" {
			w.WriteHeader(403)
			return
		}
		w.Write([]byte(`{"items":[{"id":{"videoId":"abc123"},"snippet":{"title":"Go &#39;dars&#39;","channelTitle":"Kanal","publishedAt":"2026-10-15T10:00:00Z"}}]}`))
	}))
	defer srv.Close()
	y := &YouTube{BaseURL: srv.URL, ServerKey: "srvkey"}
	out, err := y.Call(context.Background(), Config{"region": "UZ"}, "youtube_search", map[string]any{"query": "go dars", "max_results": float64(50), "order": "date"})
	if err != nil || !strings.Contains(out, "Go 'dars' — Kanal (2026-10-15)") || !strings.Contains(out, "https://youtu.be/abc123") {
		t.Fatalf("%q %v", out, err)
	}
	for _, want := range []string{"key=srvkey", "maxResults=10", "order=date", "regionCode=UZ", "q=go+dars"} {
		if !strings.Contains(gotQ, want) {
			t.Fatalf("%s yo'q: %s", want, gotQ)
		}
	}
	if _, err := y.Call(context.Background(), Config{"api_key": "bad"}, "", map[string]any{"query": "x"}); err == nil || !strings.Contains(err.Error(), "kalit") {
		t.Fatalf("403: %v", err)
	}
	if _, err := (&YouTube{BaseURL: srv.URL}).Call(context.Background(), Config{}, "", map[string]any{"query": "x"}); err == nil {
		t.Fatal("kalitsiz")
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
