package main

import "testing"

func TestNormalizeLoopbackURL(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  string
		ok    bool
	}{
		{"ipv4", "http://127.0.0.1:9876/", "http://127.0.0.1:9876", true},
		{"localhost", "http://localhost:9876", "http://localhost:9876", true},
		{"ipv6", "http://[::1]:9876", "http://[::1]:9876", true},
		{"default port", "http://127.0.0.1", "http://127.0.0.1", true},
		{"remote host", "http://example.com:9876", "", false},
		{"https", "https://127.0.0.1:9876", "", false},
		{"credentials", "http://user@127.0.0.1:9876", "", false},
		{"query", "http://127.0.0.1:9876/?x=1", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeLoopbackURL(test.input)
			if test.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !test.ok && err == nil {
				t.Fatal("expected an error")
			}
			if got != test.want {
				t.Fatalf("got %q want %q", got, test.want)
			}
		})
	}
}
