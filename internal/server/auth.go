package server

import (
	"net/http"
	"strings"
)

// wsProtocolPrefix carries a WebSocket ticket in the subprotocol header.
//
// A browser cannot set request headers on a WebSocket handshake, so the
// credential has to travel either in the URL (visible to proxies, browser
// history and Referer) or in Sec-WebSocket-Protocol, which the browser does
// control. The prefix keeps the value recognisable and lets the server echo it
// back on upgrade, which RFC 6455 requires.
const wsProtocolPrefix = "warpmesh-ticket."

// sessionCookieName holds the console session token as an HttpOnly cookie.
const sessionCookieName = "warpmesh_session"

// sessionCookiePath scopes the cookie to the whole console.
const sessionCookiePath = "/"

// setSessionCookie writes the session token as a SameSite=Strict cookie.
//
// Strict is the CSRF defence: the browser will not attach it to any
// cross-site request, so an attacker's page cannot drive the API even though
// the cookie is sent automatically.
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     sessionCookiePath,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearSessionCookie expires the session cookie.
func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     sessionCookiePath,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// requestToken returns the credential presented by a request, if any.
//
// The cookie is the preferred source. The Authorization header and the legacy
// ?token= parameter stay supported so CLI clients and the existing test suite
// keep working; the browser frontend no longer uses either.
func requestToken(r *http.Request) string {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

// checkCSRF rejects a state-changing request that a browser clearly sent from
// another origin. It complements the SameSite=Strict cookie: header-authenticated
// callers (CLI, tests) send no Cookie and are unaffected.
func checkCSRF(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if _, err := r.Cookie(sessionCookieName); err != nil {
		// No cookie ⇒ the request was authenticated by header or query, which a
		// cross-site page cannot forge.
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Non-browser client, or a same-origin form post. SameSite=Strict means
		// a cross-site request would not have carried the cookie at all.
		return true
	}
	return sameOriginCheck(r)
}

// wsTicketFromRequest extracts a ticket presented via the subprotocol header.
func wsTicketFromRequest(r *http.Request) string {
	for _, header := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, part := range strings.Split(header, ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, wsProtocolPrefix) {
				return strings.TrimPrefix(part, wsProtocolPrefix)
			}
		}
	}
	return ""
}

// authorizeWS authenticates a browser WebSocket upgrade and enforces the
// per-device grant for the session kind.
//
// It accepts either a single-use ticket (the browser path) or any credential
// requestToken understands (CLI and tests). On success it returns the actor and
// writes the subprotocol echo the WebSocket handshake requires.
func (s *Server) authorizeWS(w http.ResponseWriter, r *http.Request, kind string) (string, bool) {
	deviceID := r.URL.Query().Get("device")
	if deviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing device"})
		return "", false
	}

	if ticket := wsTicketFromRequest(r); ticket != "" {
		entry, ok := s.tickets.consume(ticket, deviceID, kind)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid or expired ticket"})
			return "", false
		}
		if !s.canAccessDevice(entry.actor, deviceID) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "device not granted"})
			return "", false
		}
		// RFC 6455: if the client offered a subprotocol the server must select
		// one from that list, otherwise the browser fails the connection.
		w.Header().Set("Sec-WebSocket-Protocol", wsProtocolPrefix+ticket)
		return entry.actor, true
	}

	actor, ok := s.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return "", false
	}
	if !s.canAccessDevice(actor, deviceID) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "device not granted"})
		return "", false
	}
	return actor, true
}

// handleWSTicket mints a single-use ticket for one WebSocket session.
//
// The ticket is bound to the requesting actor, the target device and the
// session kind, and expires within a minute, so leaking one log line no longer
// leaks a 24 hour session credential.
func (s *Server) handleWSTicket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	actor, ok := s.authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	deviceID := r.URL.Query().Get("device")
	if deviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing device"})
		return
	}
	kind := r.URL.Query().Get("kind")
	switch kind {
	case "terminal", "desktop", "files":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "kind must be terminal, desktop or files"})
		return
	}
	if !s.canAccessDevice(actor, deviceID) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "device not granted"})
		return
	}
	ticket, err := s.tickets.issue(actor, deviceID, kind)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not issue ticket"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":    ticket,
		"protocol":  wsProtocolPrefix + ticket,
		"expiresIn": int(webSocketTicketTTL.Seconds()),
	})
}
