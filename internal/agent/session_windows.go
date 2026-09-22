//go:build windows

package agent

import (
	"os"
	"sync"

	"github.com/UserExistsError/conpty"

	"github.com/pcc-258/pylon/internal/protocol"
)

// TermSession wraps a Windows ConPTY-backed shell.
type TermSession struct {
	cpty   *conpty.ConPty
	mu     sync.Mutex
	closed bool
}

func startTerminalSession(write func(protocol.Message) error, sessionID, shell string, cols, rows int) (*TermSession, error) {
	if shell == "" {
		shell = "cmd.exe"
	}
	c, err := conpty.Start(
		shell,
		conpty.ConPtyDimensions(cols, rows),
		conpty.ConPtyEnv(os.Environ()),
	)
	if err != nil {
		return nil, err
	}
	session := &TermSession{cpty: c}
	go session.readLoop(write, sessionID)
	return session, nil
}

func (t *TermSession) readLoop(write func(protocol.Message) error, sessionID string) {
	buf := make([]byte, 4096)
	for {
		n, err := t.cpty.Read(buf)
		if n > 0 {
			_ = write(protocol.Message{
				Type:      protocol.TypeTermOutput,
				SessionID: sessionID,
				Data:      protocol.EncodeData(buf[:n]),
			})
		}
		if err != nil {
			_ = write(protocol.Message{Type: protocol.TypeTermExit, SessionID: sessionID, Code: 0})
			_ = t.Close()
			return
		}
	}
}

// Input writes terminal input to the ConPTY.
func (t *TermSession) Input(data string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	_, err := t.cpty.Write([]byte(data))
	return err
}

// Resize changes the ConPTY window size.
func (t *TermSession) Resize(cols, rows int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	return t.cpty.Resize(cols, rows)
}

// Close releases the pseudo console and its process.
func (t *TermSession) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	return t.cpty.Close()
}
