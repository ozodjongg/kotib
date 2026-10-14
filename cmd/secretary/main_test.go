package main

import (
	"database/sql"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestListenAddr(t *testing.T) {
	cases := []struct {
		listen, port, want string
		bad                bool
	}{
		{"", "", "127.0.0.1:8080", false},
		{"", "3000", "0.0.0.0:3000", false},
		{"127.0.0.1:9000", "3000", "127.0.0.1:9000", false}, // aniq LISTEN ustun
		{"", "abc", "", true},
		{"", "70000", "", true},
	}
	for _, c := range cases {
		got, err := listenAddr(c.listen, c.port)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("listenAddr(%q,%q) = %q, %v", c.listen, c.port, got, err)
		}
	}
}

func TestParseIDs(t *testing.T) {
	ids, err := parseIDs(" 12, 34 ,")
	if err != nil || !ids[12] || !ids[34] || len(ids) != 2 {
		t.Fatal(ids, err)
	}
	if _, err := parseIDs("12,abc"); err == nil {
		t.Fatal("noto'g'ri ID rad etilishi kerak")
	}
	if _, err := parseIDs("-5"); err == nil {
		t.Fatal("manfiy ID rad etilishi kerak")
	}
}

func TestIntEnv(t *testing.T) {
	t.Setenv("X_TEST", "")
	if n, _ := intEnv("X_TEST", 7, 0, 10); n != 7 {
		t.Fatal(n)
	}
	t.Setenv("X_TEST", "11")
	if _, err := intEnv("X_TEST", 7, 0, 10); err == nil {
		t.Fatal("oraliqdan tashqari")
	}
}

func TestInstanceLockOneAtATime(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL yo'q")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	lost := func() { t.Error("qulf yo'qolmasligi kerak") }

	gateA := make(chan struct{})
	releaseA := holdInstanceLock(db, gateA, log, lost)
	select {
	case <-gateA:
	case <-time.After(5 * time.Second):
		t.Fatal("birinchi nusxa qulfni olishi kerak")
	}
	gateB := make(chan struct{})
	releaseB := holdInstanceLock(db, gateB, log, lost)
	defer releaseB()
	select {
	case <-gateB:
		t.Fatal("ikkinchi nusxa birinchisi ishlayotganda ulanmasligi kerak")
	case <-time.After(3 * time.Second):
	}
	releaseA() // eski nusxa to'xtadi
	select {
	case <-gateB:
	case <-time.After(5 * time.Second):
		t.Fatal("eski nusxa to'xtagach yangisi qulfni olishi kerak")
	}
}
