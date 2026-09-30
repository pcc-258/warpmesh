package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestNoiseFilterSuppressesScannerHandshakes pins the behaviour that keeps the
// relay log readable on a public IP.
func TestNoiseFilterSuppressesScannerHandshakes(t *testing.T) {
	const ourDomain = "warpmesh.ddns.net"
	cases := []struct {
		name string
		line string
		want bool // true = suppressed
	}{
		{
			"unknown host in another namespace",
			"http: TLS handshake error from 27.151.65.167:36809: no certificate available for 'm.taodouyu.com'",
			true,
		},
		{
			"bare IP handshake suggests SNI",
			"http: TLS handshake error from 1.2.3.4:5: no certificate available for '172.25.14.90'",
			true,
		},
		{
			"our own subdomain is never noise",
			"http: TLS handshake error from 1.2.3.4:5: no certificate available for 'warpmesh.ddns.net'",
			false,
		},
		{
			"another host under ddns.net is still ours to worry about",
			"http: TLS handshake error from 1.2.3.4:5: no certificate available for 'other.ddns.net'",
			false,
		},
		{
			"plain HTTP hitting the TLS port",
			"http: TLS handshake error from 1.2.3.4:5: client sent an HTTP request to an HTTPS server",
			true,
		},
		{
			"client rejected our TLS version",
			"http: TLS handshake error from 1.2.3.4:5: remote error: tls: protocol version not supported",
			true,
		},
		{
			"client offered no shared cipher",
			"http: TLS handshake error from 1.2.3.4:5: remote error: tls: no cipher suite supported",
			true,
		},
		{
			"a client handshake failure is actionable",
			"http: TLS handshake error from 1.2.3.4:5: remote error: tls: unknown certificate authority",
			false,
		},
		{
			"unrelated server errors pass through",
			"http: panic serving 1.2.3.4:5: runtime error",
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := &noiseFilteringWriter{w: &buf, domain: ourDomain}
			n, err := w.Write([]byte(tc.line))
			if err != nil {
				t.Fatal(err)
			}
			// Write must always report the full length, suppressed or not.
			if n != len(tc.line) {
				t.Fatalf("Write returned %d, want %d", n, len(tc.line))
			}
			suppressed := buf.Len() == 0
			if suppressed != tc.want {
				t.Fatalf("suppressed=%v, want %v (written=%q)", suppressed, tc.want, buf.String())
			}
			if !tc.want && !strings.Contains(buf.String(), tc.line) {
				t.Fatalf("expected the line to be written verbatim, got %q", buf.String())
			}
		})
	}
}

func TestRegistrableDomain(t *testing.T) {
	cases := map[string]string{
		"warpmesh.ddns.net": "ddns.net",
		"WWW.Taodouyu.com":  "taodouyu.com",
		"":                  "",
		"localhost":         "",
		"a.b.c.example.io":  "example.io",
	}
	for in, want := range cases {
		if got := registrableDomain(in); got != want {
			t.Fatalf("registrableDomain(%q) = %q, want %q", in, got, want)
		}
	}
}
