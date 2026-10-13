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
