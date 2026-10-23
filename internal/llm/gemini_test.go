package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGeminiReply(t *testing.T) {
	var gotKey, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-goog-api-key")
		gotPath = r.URL.String()
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":" Yaxshi, rahmat! "}]}}]}`))
	}))
	defer srv.Close()

	g := NewGemini("SECRET", "test-model")
	g.BaseURL = srv.URL
	out, err := g.Reply(context.Background(), Request{
		OwnerName: "Ali", Persona: "hazil", Language: "uz",
		History:  []Turn{{FromOwner: false, Text: "salom"}, {FromOwner: true, Text: "vaalaykum"}},
		Incoming: "qalaysan?",
	})
	if err != nil || out != "Yaxshi, rahmat!" {
		t.Fatalf("%q %v", out, err)
	}
	if gotKey != "SECRET" || strings.Contains(gotPath, "SECRET") {
		t.Fatalf("kalit faqat sarlavhada bo'lishi kerak: key=%q url=%q", gotKey, gotPath)
	}
	if !strings.Contains(gotPath, "models/test-model:generateContent") {
		t.Fatalf("yo'l: %s", gotPath)
	}
	raw, _ := json.Marshal(gotBody)
	if !strings.Contains(string(raw), "KONTAKT_XABARI") {
		t.Fatal("kontakt xabari o'ralmagan")
	}
}

func TestGeminiErrorDoesNotLeakBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte("quota SECRET-DETAIL"))
	}))
	defer srv.Close()
	g := NewGemini("k", "m")
	g.BaseURL = srv.URL
	g.RetryDelay = time.Millisecond
	_, err := g.Reply(context.Background(), Request{Incoming: "x"})
	if err == nil || strings.Contains(err.Error(), "SECRET-DETAIL") {
		t.Fatalf("xato tanani sizdirmasligi kerak: %v", err)
	}
}

func TestBuildContentsMergesSameRoleAndStartsWithUser(t *testing.T) {
	cs := buildContents(Request{
		History:  []Turn{{FromOwner: true, Text: "a"}, {FromOwner: true, Text: "b"}},
		Incoming: "c",
	})
	if cs[0]["role"] != "user" {
		t.Fatal("birinchi rol user bo'lishi kerak")
	}
	for i := 1; i < len(cs); i++ {
		if cs[i]["role"] == cs[i-1]["role"] {
			t.Fatal("ketma-ket bir xil rol")
		}
	}
}

func TestThinkingConfigPerModel(t *testing.T) {
	cases := map[string]string{
		"gemini-3.5-flash-lite": `{"thinkingLevel":"minimal"}`,
		"gemini-2.5-flash":      `{"thinkingBudget":0}`,
		"gemini-2.5-pro":        `{"thinkingBudget":128}`,
		"gemini-2.0-flash":      `null`,
	}
	for model, want := range cases {
		b, _ := json.Marshal(thinkingConfig(model))
		if string(b) != want {
			t.Errorf("%s: %s, kutilgan %s", model, b, want)
		}
	}
}

func TestGeminiRetriesWithoutThinkingOn400(t *testing.T) {
	var calls int
	var lastHadThinking bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		lastHadThinking = strings.Contains(string(b), "thinkingConfig")
		if lastHadThinking {
			w.WriteHeader(400)
			return
		}
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"o'ylayapman","thought":true},{"text":"Salom!"}]}}]}`))
	}))
	defer srv.Close()
	g := NewGemini("k", "gemini-3.5-flash-lite")
	g.BaseURL = srv.URL
	out, err := g.Reply(context.Background(), Request{Incoming: "x"})
	if err != nil || out != "Salom!" || calls != 2 || lastHadThinking {
		t.Fatalf("out=%q err=%v calls=%d", out, err, calls)
	}
}

func TestGeminiRetriesOnTimeout(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			time.Sleep(300 * time.Millisecond) // birinchi urinish vaqti tugaydi
		}
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`))
	}))
	defer srv.Close()
	g := NewGemini("k", "m")
	g.BaseURL = srv.URL
	g.HTTP.Timeout = 100 * time.Millisecond
	out, err := g.Reply(context.Background(), Request{Incoming: "x"})
	if err != nil || out != "ok" || calls.Load() != 2 {
		t.Fatalf("out=%q err=%v calls=%d", out, err, calls.Load())
	}
}

func TestGeminiSendsAudio(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"Eshitdim"}]}}]}`))
	}))
	defer srv.Close()
	g := NewGemini("k", "m")
	g.BaseURL = srv.URL
	out, err := g.Reply(context.Background(), Request{Incoming: "🎤 Ovozli xabar", Audio: &Audio{MIME: "audio/ogg", Data: []byte("OGG")}})
	if err != nil || out != "Eshitdim" {
		t.Fatal(out, err)
	}
	if !strings.Contains(raw, `"inlineData":{"data":"T0dH","mimeType":"audio/ogg"}`) || !strings.Contains(raw, "ovozli xabar") {
		t.Fatalf("audio yuborilmadi: %s", raw)
	}
}

func TestSystemPromptAboutAndRoutine(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Tashkent")
	r := Request{
		About: "O'qituvchiman, 5-maktabda ingliz tilidan dars beraman.",
		Busy:  "darsda", BusyUntil: "15:00",
		Now: time.Date(2026, 10, 13, 12, 4, 0, 0, time.UTC), Loc: loc,
	}
	p := SystemPrompt(r)
	for _, want := range []string{"HOZIR: seshanba, 17:04", "darsda, taxminan 15:00 gacha", "5-maktabda", "<<<EGASI_HAQIDA"} {
		if !strings.Contains(p, want) {
			t.Errorf("promptda %q yo'q:\n%s", want, p)
		}
	}
	// Qo'lda qo'yilgan holat kun tartibidan ustun.
	r.Away = "rulda"
	if p := SystemPrompt(r); strings.Contains(p, "darsda") || !strings.Contains(p, "EGASI HOZIR: rulda") {
		t.Errorf("away ustun emas:\n%s", p)
	}
}

func TestIsNoReply(t *testing.T) {
	for _, s := range []string{NoReply, " [javobsiz] ", "JAVOBSIZ", "\"[JAVOBSIZ]\"", "[JAVOBSIZ]."} {
		if !IsNoReply(s) {
			t.Errorf("%q", s)
		}
	}
	for _, s := range []string{"", "Javobsiz qolmaysiz", "ok"} {
		if IsNoReply(s) {
			t.Errorf("%q", s)
		}
	}
	if !strings.Contains(SystemPrompt(Request{}), NoReply) {
		t.Error("system prompt javobsiz belgisini tushuntirmaydi")
	}
}
