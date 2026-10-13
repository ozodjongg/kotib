package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
