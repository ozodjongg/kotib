package plugin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTelegramFeedOwner(t *testing.T) {
	var gotOwner int64
	var gotKind string
	feed := func(_ context.Context, owner int64, kind string, since time.Time, max int) ([]FeedChat, error) {
		gotOwner, gotKind = owner, kind
		if time.Since(since) < 47*time.Hour || max != 5 {
			t.Errorf("since/max: %v %d", since, max)
		}
		return []FeedChat{{Title: "Kun.uz", Kind: "channel", Posts: []FeedPost{
			{At: time.Now(), Text: "Muhim   yangilik\n" + strings.Repeat("x", 900), Link: "https://t.me/kunuz/1"}}}}, nil
	}
	r := Default(Options{TelegramFeed: feed})
	decls, call := r.Toolset([]Enabled{{ID: "telegram", Owner: 42}})
	if len(decls) != 1 || decls[0].Name != "telegram_feed" {
		t.Fatalf("%+v", decls)
	}
	out, err := call(context.Background(), "telegram_feed", map[string]any{"kind": "x", "hours": float64(48), "max_chats": float64(5)})
	if err != nil || gotOwner != 42 || gotKind != "channels" || !strings.Contains(out, "### Kun.uz (1 ta xabar)") ||
		!strings.Contains(out, "Muhim yangilik") || !strings.Contains(out, "(https://t.me/kunuz/1)") || strings.Contains(out, strings.Repeat("x", 700)) {
		t.Fatalf("%q %v", out, err)
	}
	// Egasiz chaqiruv (boshqa egaga tegishli emas) — rad etiladi.
	if _, err := (&Telegram{Feed: feed}).Call(context.Background(), nil, "", nil); err == nil {
		t.Fatal("egasiz")
	}
	if _, ok := Default(Options{}).Get("telegram"); ok {
		t.Fatal("Feed'siz Telegram plagini bo'lmasligi kerak")
	}
	empty := &Telegram{Feed: func(context.Context, int64, string, time.Time, int) ([]FeedChat, error) { return nil, nil }}
	if out, _ := empty.Call(WithOwner(context.Background(), 1), nil, "", map[string]any{"kind": "groups"}); !strings.Contains(out, "guruhlaringizda yangi xabar yo'q") {
		t.Fatalf("bo'sh: %q", out)
	}
}

func TestPrayer(t *testing.T) {
	var islomFail bool
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		switch {
		case strings.HasPrefix(r.URL.Path, "/islom/"):
			if islomFail {
				w.WriteHeader(502)
				return
			}
			w.Write([]byte(`{"region":"Samarqand","hijri_date":{"month":"rabiul avval","day":24},
				"times":{"tong_saharlik":"05:21","quyosh":"06:42","peshin":"12:27","asr":"15:51","shom_iftor":"18:06","hufton":"19:22"}}`))
		case strings.HasPrefix(r.URL.Path, "/aladhan/timingsByCity/"):
			w.Write([]byte(`{"data":{"timings":{"Fajr":"05:10 (+05)","Sunrise":"06:40","Dhuhr":"12:25","Asr":"15:50","Maghrib":"18:05","Isha":"19:30"}}}`))
		}
	}))
	defer srv.Close()
	p := &Prayer{IslomURL: srv.URL + "/islom", AladhanURL: srv.URL + "/aladhan"}
	out, err := p.Call(context.Background(), Config{"region": "Toshkent"}, "", map[string]any{"region": "samarqand"})
	if err != nil || !strings.Contains(out, "Samarqand namoz vaqtlari (hijriy: 24 rabiul avval)") || !strings.Contains(out, "• Bomdod (saharlik) — 05:21") ||
		!strings.Contains(out, "• Xufton — 19:22") || !strings.Contains(paths[0], "/islom/present/day?region=Samarqand") {
		t.Fatalf("%q %v %v", out, err, paths)
	}
	// Ertangi kun — boshqa endpoint; islomapi ishlamasa aladhan (Hanafiy).
	islomFail = true
	out, err = p.Call(context.Background(), Config{"region": "Farg'ona"}, "", map[string]any{"day": "tomorrow"})
	last := paths[len(paths)-1]
	if err != nil || !strings.Contains(out, "Ertaga") || !strings.Contains(out, "Farg'ona") || !strings.Contains(out, "• Bomdod (saharlik) — 05:10\n") ||
		!strings.Contains(out, "taxminiy") || !strings.Contains(last, "city=Fergana") || !strings.Contains(last, "school=1") || !strings.Contains(paths[len(paths)-2], "/islom/daily?") {
		t.Fatalf("fallback: %q %v %v", out, err, paths)
	}
}

const rss = `<?xml version="1.0"?><rss><channel>
<item><title>Yangi &amp; muhim qaror</title><link>https://kun.uz/1</link><description>&lt;p&gt;Prezident <b>farmon</b> imzoladi&lt;/p&gt;</description><pubDate>%s</pubDate></item>
<item><title>Eski xabar</title><link>https://kun.uz/0</link><description>x</description><pubDate>Mon, 02 Jan 2006 15:04:05 +0500</pubDate></item>
</channel></rss>`

func TestNews(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.WriteHeader(500)
			return
		}
		fmt.Fprintf(w, rss, time.Now().Add(-time.Hour).Format(time.RFC1123Z))
	}))
	defer srv.Close()
	n := &News{URLs: map[string]string{"kun": srv.URL + "/kun", "gazeta": srv.URL + "/bad", "daryo": srv.URL + "/kun"}}
	out, err := n.Call(context.Background(), Config{"sources": "kun.uz, gazeta"}, "", nil)
	if err != nil || !strings.Contains(out, "1. Yangi & muhim qaror") || !strings.Contains(out, "Prezident farmon imzoladi") ||
		strings.Contains(out, "Eski xabar") || !strings.Contains(out, "Ochilmagan manbalar: Gazeta.uz") || strings.Contains(out, "<b>") {
		t.Fatalf("%q %v", out, err)
	}
	if out, _ := n.Call(context.Background(), Config{}, "", map[string]any{"query": "futbol"}); !strings.Contains(out, "topilmadi") {
		t.Fatalf("filtr: %q", out)
	}
	if out, _ := n.Call(context.Background(), Config{}, "", map[string]any{"sources": "daryo,kun"}); strings.Count(out, "Yangi & muhim") != 2 {
		t.Fatalf("ikki manba: %q", out)
	}
}

func TestSheets(t *testing.T) {
	const id = "1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer AT" {
			w.WriteHeader(401)
			return
		}
		paths = append(paths, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		if strings.Contains(r.URL.Path, "/values/") {
			w.Write([]byte(`{"values":[["Sana","Mijoz","Summa"],["16.10","Aziz",150000],["16.10","Vali","90000"]]}`))
			return
		}
		w.Write([]byte(`{"properties":{"title":"Buyurtmalar"},"sheets":[{"properties":{"title":"Oktyabr"}},{"properties":{"title":"Ombor"}}]}`))
	}))
	defer srv.Close()
	s := &Sheets{BaseURL: srv.URL, Token: func(context.Context, string) (string, error) { return "AT", nil }}
	cfg := Config{KeyRefreshToken: "RT", "urls": "https://docs.google.com/spreadsheets/d/" + id + "/edit#gid=0"}
	out, err := s.Call(context.Background(), cfg, "", map[string]any{"sheet": "ombor", "max_rows": float64(10)})
	if err != nil || !strings.Contains(out, "Jadval «Buyurtmalar», varaq «Ombor» (varaqlar: Oktyabr, Ombor)") || !strings.Contains(out, "16.10 | Aziz | 150000") {
		t.Fatalf("%q %v", out, err)
	}
	if v, _ := url.PathUnescape(paths[1]); !strings.Contains(v, "/"+id+"/values/'Ombor'!A1:Z11") {
		t.Fatalf("range: %s", v)
	}
	if _, err := s.Call(context.Background(), Config{KeyRefreshToken: "RT"}, "", nil); err == nil || !strings.Contains(err.Error(), "havolasi") {
		t.Fatalf("havolasiz: %v", err)
	}
	if ids := sheetIDs("a " + id + ", https://docs.google.com/spreadsheets/d/" + id + "x/edit x y z"); len(ids) != 2 {
		t.Fatalf("ids: %v", ids)
	}
	if !(sheetsPlugin(Options{})).Info().Soon {
		t.Fatal("Google'siz — tez orada")
	}
	// Ulanish + sozlama maydonlari: token saqlanadi, havola Mini App'dan.
	c, err := Clean(s.Info(), Config{"urls": " " + id + " ", KeyRefreshToken: "hack"}, Config{KeyRefreshToken: "RT", KeyAccount: "a@x.uz"})
	if err != nil || c[KeyRefreshToken] != "RT" || c["urls"] != id {
		t.Fatalf("clean: %v %v", c, err)
	}
}

func TestAmoCRM(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Path+"?"+r.URL.RawQuery)
		if r.Header.Get("Authorization") != "Bearer TOK" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/v4/leads":
			fmt.Fprintf(w, `{"_embedded":{"leads":[{"name":"Mebel buyurtma","price":2500000,"created_at":%d}]}}`, time.Now().Unix())
		case "/api/v4/tasks":
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()
	a := &AmoCRM{BaseURL: srv.URL}
	cfg := Config{"subdomain": "kompaniya", "token": "TOK"}
	out, err := a.Call(context.Background(), cfg, "", map[string]any{"what": "leads", "days": float64(2)})
	if err != nil || !strings.Contains(out, "1 ta yangi lid") || !strings.Contains(out, "Mebel buyurtma — 2500000") {
		t.Fatalf("%q %v", out, err)
	}
	if out, err := a.Call(context.Background(), cfg, "", map[string]any{"what": "tasks"}); err != nil || !strings.Contains(out, "vazifa yo'q") {
		t.Fatalf("204: %q %v", out, err)
	}
	if _, err := a.Call(context.Background(), Config{"subdomain": "kompaniya", "token": "bad"}, "", map[string]any{"what": "leads"}); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("401: %v", err)
	}
	for _, bad := range []string{"evil.com/x", "a.b", "", "-x"} {
		if a.Validate(Config{"subdomain": bad}) == nil {
			t.Fatalf("%q rad etilishi kerak", bad)
		}
	}
}

func TestBitrix24(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/rest/1/abc123/") {
			w.WriteHeader(401)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "crm.deal.list.json"):
			w.Write([]byte(`{"result":[{"TITLE":"Ofis jihozlari","OPPORTUNITY":"12000000","CURRENCY_ID":"UZS","DATE_CREATE":"2026-10-16T10:00:00+05:00"}]}`))
		case strings.HasSuffix(r.URL.Path, "tasks.task.list.json"):
			w.Write([]byte(`{"result":{"tasks":[{"title":"Mijozga qo'ng'iroq","deadline":"2020-01-01T10:00:00+05:00"}]}}`))
		}
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	x := &Bitrix24{AllowHost: host}
	cfg := Config{"webhook": srv.URL + "/rest/1/abc123/"}
	out, err := x.Call(context.Background(), cfg, "", map[string]any{"what": "deals"})
	if err != nil || !strings.Contains(out, "Ofis jihozlari — 12000000 UZS") {
		t.Fatalf("%q %v", out, err)
	}
	if out, err := x.Call(context.Background(), cfg, "", map[string]any{"what": "tasks"}); err != nil || !strings.Contains(out, "Mijozga qo'ng'iroq") || !strings.Contains(out, "muddati o'tgan") {
		t.Fatalf("tasks: %q %v", out, err)
	}
	// SSRF: faqat *.bitrix24.* va /rest/<id>/<kalit>/.
	real := &Bitrix24{}
	for _, ok := range []string{"https://kompaniya.bitrix24.ru/rest/1/abc123/", "https://my-co.bitrix24.uz/rest/15/Zx9/"} {
		if err := real.Validate(Config{"webhook": ok}); err != nil {
			t.Fatalf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"http://kompaniya.bitrix24.ru/rest/1/abc/", "https://169.254.169.254/rest/1/abc/", "https://evil.com/rest/1/abc/",
		"https://kompaniya.bitrix24.ru.evil.com/rest/1/abc/", "https://kompaniya.bitrix24.ru/admin/", "https://u:p@kompaniya.bitrix24.ru/rest/1/abc/",
		"https://kompaniya.bitrix24.ru/rest/1/abc/?x=1", srv.URL + "/rest/1/abc123/",
	} {
		var inv *ErrInvalid
		if err := real.Validate(Config{"webhook": bad}); !errors.As(err, &inv) {
			t.Fatalf("%q rad etilishi kerak", bad)
		}
	}
}
