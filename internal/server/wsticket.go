package server

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// webSocketTicketTTL is deliberately short: a ticket is minted immediately
// before a WebSocket is opened and consumed on first use.
const webSocketTicketTTL = 60 * time.Second

// wsTicket is a single-use credential that authorizes exactly one WebSocket
// upgrade for one resource.
//
// Browsers cannot set headers on a WebSocket handshake, which is why the
// session token used to be passed as a ?token= query parameter: it then ends up
// in proxy logs, browser history and Referer headers. A ticket removes that
// exposure while still letting the frontend authenticate the upgrade.
type wsTicket struct {
	actor     string
	device    string
	kind      string
	expiresAt time.Time
}

// ticketStore holds outstanding tickets in memory. They are intentionally not
// persisted: they are valid for seconds and lost tickets are simply reissued.
type ticketStore struct {
	mu      sync.Mutex
	tickets map[string]wsTicket
}

func newTicketStore() *ticketStore {
	return &ticketStore{tickets: make(map[string]wsTicket)}
}

// issue mints a ticket for one actor, device and session kind.
func (t *ticketStore) issue(actor, device, kind string) (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	t.mu.Lock()
	t.tickets[token] = wsTicket{
		actor:     actor,
		device:    device,
		kind:      kind,
		expiresAt: time.Now().Add(webSocketTicketTTL),
	}
	t.mu.Unlock()
	return token, nil
}

// consume validates and removes a ticket. A ticket is accepted at most once,
// which bounds the value of one leaked from a log line.
func (t *ticketStore) consume(token, device, kind string) (wsTicket, bool) {
	t.mu.Lock()
	ticket, ok := t.tickets[token]
	delete(t.tickets, token)
	t.mu.Unlock()
	if !ok {
		return wsTicket{}, false
	}
	if time.Now().After(ticket.expiresAt) {
		return wsTicket{}, false
	}
	// A ticket is bound to the resource it was issued for, so it cannot be
	// replayed against a different device.
	if ticket.device != device || ticket.kind != kind {
		return wsTicket{}, false
	}
	return ticket, true
}

// sweep drops expired tickets. Without it the map grows for the process life.
func (t *ticketStore) sweep() {
	now := time.Now()
	t.mu.Lock()
	for token, ticket := range t.tickets {
		if now.After(ticket.expiresAt) {
			delete(t.tickets, token)
		}
	}
	t.mu.Unlock()
}
