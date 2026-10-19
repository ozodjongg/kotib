package plugin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDrive(t *testing.T) {
	const doc, sheet, pdf = "1DocDocDocDocDoc", "1SheetSheetSheet", "1PdfPdfPdfPdfPdf"
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer AT" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/files":
			queries = append(queries, r.URL.RawQuery)
			w.Write([]byte(`{"files":[{"id":"` + doc + `","name":"Kun rejasi","mimeType":"application/vnd.google-apps.document",` +
				`"modifiedTime":"2026-10-18T05:00:00Z","webViewLink":"https://docs.google.com/document/d/x"}]}`))
		case r.URL.Path == "/files/"+doc+"/export":
			if r.URL.Query().Get("mimeType") != "text/plain" {
				w.WriteHeader(400)
				return
			}
			w.Write([]byte("\ufeff1. Hisobot\n2. Uchrashuv " + strings.Repeat("x", 9000)))
		case r.URL.Path == "/files/"+sheet+"/export":
			w.Write([]byte("Sana,Summa\n16.10,150000\n"))
		case r.URL.Path == "/files/"+doc:
			w.Write([]byte(`{"name":"Kun rejasi","mimeType":"application/vnd.google-apps.document"}`))
		case r.URL.Path == "/files/"+sheet:
			w.Write([]byte(`{"name":"Savdo","mimeType":"application/vnd.google-apps.spreadsheet"}`))
		case r.URL.Path == "/files/"+pdf:
			w.Write([]byte(`{"name":"Shartnoma.pdf","mimeType":"application/pdf"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	d := drivePlugin(Options{DriveURL: srv.URL, DriveToken: func(context.Context, string) (string, error) { return "AT", nil }})
	if d.Info().Soon || d.Info().Connect != "google" {
		t.Fatalf("%+v", d.Info())
	}
	cfg := Config{KeyRefreshToken: "RT"}
	ctx := context.Background()

	out, err := d.Call(ctx, cfg, "drive_search", map[string]any{"query": "o'g'il", "days": float64(7)})
	if err != nil || !strings.Contains(out, "Kun rejasi (hujjat, o'zgargan 18.10.2026 10:00) — ID: "+doc) {
		t.Fatalf("%q %v", out, err)
	}
	if !strings.Contains(queries[0], "fullText+contains") || !strings.Contains(queries[0], `o%5C%27g%5C%27il`) ||
		!strings.Contains(queries[0], "modifiedTime") || strings.Contains(queries[0], "orderBy") {
		t.Fatalf("so'rov: %s", queries[0])
	}
	if _, err := d.Call(ctx, cfg, "drive_search", nil); err != nil || !strings.Contains(queries[1], "orderBy=modifiedTime+desc") {
		t.Fatalf("oxirgilar: %s %v", queries[1], err)
	}

	out, err = d.Call(ctx, cfg, "drive_read", map[string]any{"file_id": doc})
	if err != nil || !strings.HasPrefix(out, "Fayl «Kun rejasi»:\n1. Hisobot") || !strings.HasSuffix(out, "(qisqartirildi)") || len(out) > maxFeedOutput+100 {
		t.Fatalf("hujjat: %q %v", out[:min(len(out), 80)], err)
	}
	if out, err := d.Call(ctx, cfg, "drive_read", map[string]any{"file_id": sheet}); err != nil || !strings.Contains(out, "16.10,150000") {
		t.Fatalf("jadval: %q %v", out, err)
	}
	if out, err := d.Call(ctx, cfg, "drive_read", map[string]any{"file_id": pdf}); err != nil || !strings.Contains(out, "o'qib bo'lmaydi") {
		t.Fatalf("pdf: %q %v", out, err)
	}
	if _, err := d.Call(ctx, cfg, "drive_read", map[string]any{"file_id": "../../etc"}); err == nil {
		t.Fatal("noto'g'ri ID qabul qilindi")
	}
	if _, err := d.Call(ctx, Config{}, "drive_search", nil); err == nil || !strings.Contains(err.Error(), "ulanmagan") {
		t.Fatalf("akkauntsiz: %v", err)
	}
	if !drivePlugin(Options{}).Info().Soon {
		t.Fatal("Google'siz — tez orada")
	}
}

func TestNotion(t *testing.T) {
	const page, db, inbox = "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d", "aaaabbbbccccddddeeeeffff00001111", "99998888777766665555444433332222"
	var appended map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ntn_abcdefghijklmnopqrstuvwxyz" || r.Header.Get("Notion-Version") != notionVersion {
			w.WriteHeader(401)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /search":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["query"] != "reja" {
				w.WriteHeader(400)
				return
			}
			w.Write([]byte(`{"results":[` +
				`{"object":"page","id":"1a2b3c4d-5e6f-7a8b-9c0d-1e2f3a4b5c6d","url":"https://www.notion.so/Reja-` + page + `","last_edited_time":"2026-10-18T05:00:00.000Z",` +
				`"properties":{"Name":{"type":"title","title":[{"plain_text":"Haftalik reja"}]}}},` +
				`{"object":"database","id":"` + db + `","url":"https://www.notion.so/` + db + `","title":[{"plain_text":"Vazifalar"}],"properties":{}}]}`))
		case "GET /pages/" + page:
			w.Write([]byte(`{"object":"page","id":"` + page + `","url":"https://www.notion.so/x","properties":{"title":{"type":"title","title":[{"plain_text":"Haftalik reja"}]}}}`))
		case "GET /blocks/" + page + "/children":
			w.Write([]byte(`{"results":[` +
				`{"type":"heading_2","heading_2":{"rich_text":[{"plain_text":"Dushanba"}]}},` +
				`{"type":"to_do","to_do":{"rich_text":[{"plain_text":"Hisobot"}],"checked":true}},` +
				`{"type":"bulleted_list_item","bulleted_list_item":{"rich_text":[{"plain_text":"Uchrashuv "},{"plain_text":"10:00"}]}},` +
				`{"type":"divider","divider":{}},` +
				`{"type":"child_page","child_page":{"title":"Arxiv"}}]}`))
		case "GET /pages/" + db:
			w.WriteHeader(404)
		case "GET /databases/" + db:
			w.Write([]byte(`{"object":"database","id":"` + db + `","title":[{"plain_text":"Vazifalar"}]}`))
		case "POST /databases/" + db + "/query":
			w.Write([]byte(`{"results":[{"object":"page","id":"r1","properties":{` +
				`"Nomi":{"type":"title","title":[{"plain_text":"Saytni yangilash"}]},` +
				`"Holat":{"type":"status","status":{"name":"Jarayonda"}},` +
				`"Muddat":{"type":"date","date":{"start":"2026-10-20"}},` +
				`"Tayyor":{"type":"checkbox","checkbox":false}}}]}`))
		case "PATCH /blocks/" + inbox + "/children":
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &appended)
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	n := &Notion{BaseURL: srv.URL}
	cfg := Config{"token": "ntn_abcdefghijklmnopqrstuvwxyz", "page": "https://www.notion.so/team/Kiruvchi-" + inbox + "?pvs=4"}
	ctx := context.Background()
	if err := n.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	if err := n.Validate(Config{"token": "abc"}); err == nil {
		t.Fatal("noto'g'ri token qabul qilindi")
	}
	if err := n.Validate(Config{"token": cfg["token"], "page": "https://www.notion.so/Reja"}); err == nil {
		t.Fatal("ID'siz havola qabul qilindi")
	}

	out, err := n.Call(ctx, cfg, "notion_search", map[string]any{"query": "reja"})
	if err != nil || !strings.Contains(out, "1. Haftalik reja (sahifa, tahrirlangan 18.10.2026 10:00) — ID: "+page) || !strings.Contains(out, "2. Vazifalar (baza") {
		t.Fatalf("qidiruv: %q %v", out, err)
	}
	out, err = n.Call(ctx, cfg, "notion_read", map[string]any{"id": "1a2b3c4d-5e6f-7a8b-9c0d-1e2f3a4b5c6d"})
	want := "Sahifa «Haftalik reja» (https://www.notion.so/x):\n## Dushanba\n☑ Hisobot\n• Uchrashuv 10:00\n📄 Arxiv\n"
	if err != nil || out != want {
		t.Fatalf("sahifa: %q %v", out, err)
	}
	out, err = n.Call(ctx, cfg, "notion_read", map[string]any{"id": db})
	if err != nil || !strings.Contains(out, "Baza «Vazifalar» — 1 qator:\n1. Saytni yangilash — Holat: Jarayonda; Muddat: 2026-10-20; Tayyor: ☐") {
		t.Fatalf("baza: %q %v", out, err)
	}

	long := strings.Repeat("a", 2500)
	if out, err := n.Call(ctx, cfg, "notion_append", map[string]any{"text": "Kun xulosasi\n" + long}); err != nil || !strings.Contains(out, "yozildi") {
		t.Fatalf("yozish: %q %v", out, err)
	}
	children, _ := appended["children"].([]any)
	if len(children) != 3 { // sarlavha + 2000 + 500 belgili bo'laklar
		t.Fatalf("bloklar: %d", len(children))
	}
	// Yozish faqat sozlangan sahifaga: sahifasiz — rad etiladi.
	if _, err := n.Call(ctx, Config{"token": cfg["token"]}, "notion_append", map[string]any{"text": "x"}); err == nil || !strings.Contains(err.Error(), "sozlanmagan") {
		t.Fatalf("sahifasiz yozish: %v", err)
	}
	if _, err := n.Call(ctx, Config{"token": "ntn_wrongwrongwrongwrongwrong"}, "notion_search", nil); err == nil || !strings.Contains(err.Error(), "rad etdi") {
		t.Fatalf("noto'g'ri token: %v", err)
	}
	if id := notionID("https://www.notion.so/My-Page-" + page); id != page {
		t.Fatalf("id: %s", id)
	}
}

func TestSport(t *testing.T) {
	today := time.Now().In(uzLoc).Format("20060102")
	var dates string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/site/v2/sports/soccer/esp.1/scoreboard":
			dates = r.URL.Query().Get("dates")
			w.Write([]byte(`{"events":[` +
				`{"date":"2026-10-18T19:00Z","competitions":[{"competitors":[` +
				`{"homeAway":"home","score":"2","team":{"displayName":"Real Madrid","shortDisplayName":"Real Madrid"}},` +
				`{"homeAway":"away","score":"1","team":{"displayName":"Sevilla","shortDisplayName":"Sevilla"}}],` +
				`"status":{"type":{"state":"post","shortDetail":"FT"}}}]},` +
				`{"date":"2026-10-19T16:30Z","competitions":[{"competitors":[` +
				`{"homeAway":"home","score":"0","team":{"displayName":"Girona","shortDisplayName":"Girona"}},` +
				`{"homeAway":"away","score":"0","team":{"displayName":"Barcelona","shortDisplayName":"Barça"}}],` +
				`"status":{"type":{"state":"pre","shortDetail":"Sun 4:30 PM"}}}]}]}`))
		case "/v2/sports/soccer/esp.1/standings":
			w.Write([]byte(`{"children":[{"name":"LaLiga","standings":{"entries":[` +
				`{"team":{"displayName":"Barcelona"},"stats":[{"name":"rank","value":2},{"name":"gamesPlayed","value":9},{"name":"wins","value":6},{"name":"ties","value":2},{"name":"losses","value":1},{"name":"points","value":20}]},` +
				`{"team":{"displayName":"Real Madrid"},"stats":[{"name":"rank","value":1},{"name":"gamesPlayed","value":9},{"name":"wins","value":7},{"name":"ties","value":1},{"name":"losses","value":1},{"name":"points","value":22}]}]}}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	s := &Sport{BaseURL: srv.URL}
	ctx := context.Background()
	cfg := Config{"league": "soccer/esp.1", "team": "Real Madrid"}

	out, err := s.Call(ctx, cfg, "sport_scores", nil)
	if err != nil || dates != today || !strings.Contains(out, "19.10 00:00 — Real Madrid 2:1 Sevilla (FT)") || strings.Contains(out, "Girona") {
		t.Fatalf("sevimli jamoa: %q %v (dates=%s)", out, err, dates)
	}
	out, err = s.Call(ctx, cfg, "sport_scores", map[string]any{"team": "barça", "from": float64(-1), "days": float64(3)})
	if err != nil || !strings.Contains(out, "19.10 21:30 — Girona : Barcelona (boshlanadi)") || !strings.Contains(dates, "-") {
		t.Fatalf("taqvim: %q %v (dates=%s)", out, err, dates)
	}
	if out, _ := s.Call(ctx, cfg, "sport_scores", map[string]any{"team": "all"}); !strings.Contains(out, "2 ta o'yin") {
		t.Fatalf("hammasi: %q", out)
	}
	if out, _ := s.Call(ctx, cfg, "sport_scores", map[string]any{"team": "Chelsea"}); !strings.Contains(out, "o'yini yo'q") {
		t.Fatalf("o'yinsiz: %q", out)
	}
	out, err = s.Call(ctx, cfg, "sport_standings", nil)
	if err != nil || !strings.Contains(out, "1. Real Madrid — 9 o'yin, 7-1-1, 22 ochko\n2. Barcelona — 9 o'yin, 6-2-1, 20 ochko") {
		t.Fatalf("jadval: %q %v", out, err)
	}
	// Ro'yxatdan tashqari yo'l (SSRF/ixtiyoriy so'rov) qabul qilinmaydi.
	if _, err := s.Call(ctx, cfg, "sport_scores", map[string]any{"league": "../../x"}); err == nil {
		t.Fatal("ixtiyoriy liga qabul qilindi")
	}
	if _, err := Clean(s.Info(), Config{"league": "soccer/xyz"}, nil); err == nil {
		t.Fatal("ro'yxatda yo'q liga saqlandi")
	}
}
