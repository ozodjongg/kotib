package main

import "testing"

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
