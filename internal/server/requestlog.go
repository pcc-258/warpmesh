package server

import (
	"bufio"
	"net"
	"net/http"
	"strings"
	"time"
)

// statusRecorder captures the response status and size so the request log can
// report them.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Hijack lets WebSocket upgrades and other connection takeovers keep working
// through the recorder.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

// Flush preserves streaming responses.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// withRequestLog records every request that is worth an operator's attention:
// mutations, authentication and authorization outcomes, and server errors.
// Successful reads (the console polls them every few seconds) are left out so
// the business log stays readable.
func (s *Server) withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// WebSocket upgrades hijack the connection and stay open for the whole
		// session; the session lifecycle is logged instead.
		if strings.HasPrefix(r.URL.Path, "/ws/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		interesting := status >= 400 ||
			(status >= 200 && status < 400 && r.Method != http.MethodGet && r.Method != http.MethodHead)
		if !interesting {
			return
		}
		path := r.URL.Path
		if q := r.URL.RawQuery; q != "" {
			// Never echo credentials back into the log.
			path += "?" + redactQuery(q)
		}
		s.logEvent("http.request",
			"ip", clientIP(r),
			"method", r.Method,
			"path", path,
			"status", status,
			"bytes", rec.bytes,
			"dur", time.Since(start).Round(time.Millisecond).String(),
		)
	})
}

// redactQuery removes credential-bearing parameters from a logged query string.
func redactQuery(raw string) string {
	parts := strings.Split(raw, "&")
	for i, p := range parts {
		key, _, found := strings.Cut(p, "=")
		if !found {
			continue
		}
		switch key {
		case "token", "password", "code", "key":
			parts[i] = key + "=REDACTED"
		}
	}
	return strings.Join(parts, "&")
}
