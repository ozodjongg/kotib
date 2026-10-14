package llm

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

func testAssist() *Assist {
	loc := time.FixedZone("Asia/Tashkent", 5*3600)
	return &Assist{
		Now: time.Date(2026, 10, 14, 10, 30, 0, 0, loc), Loc: loc,
		Busy:        []Interval{{Start: time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 15, 11, 0, 0, 0, time.UTC)}},
		ContactName: "Aziz", ContactNote: "hamkor",
	}
}

func TestParseAssist(t *testing.T) {
	a := testAssist()
	r, err := parseAssist(`{"reply":"Maqul, aniqlab yozaman","event":{"title":" Aziz bilan\nuchrashuv ","start":"2026-10-15T15:00","duration_min":30},"task":{"title":"Hujjat yuborish","due":"2026-10-16"}}`, a)
	if err != nil || r.Text != "Maqul, aniqlab yozaman" {
		t.Fatal(r, err)
	}
	if r.Event == nil || r.Event.Title != "Aziz bilan uchrashuv" || r.Event.Duration != 30*time.Minute ||
		!r.Event.Start.Equal(time.Date(2026, 10, 15, 15, 0, 0, 0, a.Loc)) {
		t.Fatalf("event: %+v", r.Event)
	}
	if r.Task == nil || r.Task.Due.Format("2006-01-02") != "2026-10-16" {
		t.Fatalf("task: %+v", r.Task)
	}
}

func TestParseAssistRejectsBadActions(t *testing.T) {
	a := testAssist()
	cases := []string{
		`{"reply":"ok","event":{"title":"x","start":"2020-01-01T10:00"}}`, // o'tmish
		`{"reply":"ok","event":{"title":"x","start":"2030-01-01T10:00"}}`, // juda uzoq
		`{"reply":"ok","event":{"title":"","start":"2026-10-15T10:00"}}`,  // nomsiz
		`{"reply":"ok","event":{"title":"x","start":"ertaga"}}`,           // format
		`{"reply":"ok","event":null,"task":null}`,
	}
	for _, c := range cases {
		r, err := parseAssist(c, a)
		if err != nil || r.Text != "ok" || r.Event != nil || r.Task != nil {
			t.Errorf("%s → %+v %v", c, r, err)
		}
	}
	if r, _ := parseAssist(`{"reply":"ok","event":{"title":"x","start":"2026-10-15T10:00","duration_min":9999}}`, a); r.Event == nil || r.Event.Duration != time.Hour {
		t.Fatal("noto'g'ri davomiylik 60 daqiqaga tushishi kerak")
	}
	if r, _ := parseAssist(`{"reply":"ok","task":{"title":"t","due":"2020-01-01"}}`, a); r.Task == nil || !r.Task.Due.IsZero() {
		t.Fatal("o'tgan muddat tashlanishi kerak, vazifa qolishi kerak")
	}
	if _, err := parseAssist(`{"reply":""}`, a); err == nil {
		t.Fatal("bo'sh javob xato")
	}
	if _, err := parseAssist(`not json`, a); err == nil {
		t.Fatal("JSON xato")
	}
}

func TestGeminiAssistSendsSchemaAndContext(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"{\"reply\":\"Salom\",\"event\":null}"}]}}]}`))
	}))
	defer srv.Close()
	g := NewGemini("k", "gemini-3.5-flash-lite")
	g.BaseURL = srv.URL
	r, err := g.Assist(context.Background(), Request{Incoming: "ertaga uchrashamizmi?", Assist: testAssist()})
	if err != nil || r.Text != "Salom" || r.Event != nil {
		t.Fatal(r, err)
	}
	gen := body["generationConfig"].(map[string]any)
	if gen["responseMimeType"] != "application/json" || gen["responseSchema"] == nil {
		t.Fatalf("gen: %v", gen)
	}
	raw, _ := json.Marshal(body["systemInstruction"])
	for _, want := range []string{"2026-10-14 10:30", "chorshanba", "15.10", "15:00–16:00", "Aziz", "hamkor"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("system prompt'da %q yo'q", want)
		}
	}
	// Oddiy Reply sxemasiz qoladi.
	if _, err := g.Reply(context.Background(), Request{Incoming: "x", Assist: testAssist()}); err != nil {
		t.Fatal(err)
	}
	if body["generationConfig"].(map[string]any)["responseSchema"] != nil {
		t.Fatal("Reply JSON sxemasini yubormasligi kerak")
	}
}
