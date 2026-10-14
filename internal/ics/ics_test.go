package ics

import (
	"strings"
	"testing"
	"time"
)

func TestBuild(t *testing.T) {
	loc := time.FixedZone("Asia/Tashkent", 5*3600)
	out := string(Build(Event{
		UID: "kotib-1-2@x", Title: "Aziz bilan uchrashuv; ofis, 2-qavat " + strings.Repeat("uzun ", 20),
		Start: time.Date(2026, 10, 15, 15, 0, 0, 0, loc), Duration: time.Hour, Note: "a\nb",
	}, time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)))
	for _, want := range []string{"DTSTART:20261015T100000Z", "DTEND:20261015T110000Z", `uchrashuv\; ofis\, 2-qavat`, `DESCRIPTION:a\nb`, "TRIGGER:-PT15M"} {
		if !strings.Contains(strings.ReplaceAll(out, "\r\n ", ""), want) {
			t.Errorf("%q yo'q", want)
		}
	}
	for _, l := range strings.Split(out, "\r\n") {
		if len(l) > 75 {
			t.Fatalf("qator 75 baytdan uzun: %q", l)
		}
	}
}

func TestSigner(t *testing.T) {
	s := &Signer{Key: []byte("k"), Base: "https://kotib.example/"}
	u := s.URL(7, 42)
	parts := strings.Split(strings.TrimPrefix(u, "https://kotib.example/ics/"), "/")
	if len(parts) != 3 {
		t.Fatal(u)
	}
	if o, id, ok := s.Verify(parts[0], parts[1], parts[2]); !ok || o != 7 || id != 42 {
		t.Fatal("imzo tekshirilmadi")
	}
	if _, _, ok := s.Verify("8", parts[1], parts[2]); ok {
		t.Fatal("boshqa ega bilan ishlamasligi kerak")
	}
	if _, _, ok := (&Signer{Key: []byte("boshqa")}).Verify(parts[0], parts[1], parts[2]); ok {
		t.Fatal("boshqa kalit")
	}
}
