//go:build !windows

package agent

import (
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"

	"github.com/pcc-258/device-relay/internal/protocol"
)

// TermSession wraps a PTY-backed shell.
type TermSession struct {
	cmd    *exec.Cmd
	pty    *os.File
	mu     sync.Mutex
	closed bool
}

func startTerminalSession(write func(protocol.Message) error, sessionID, shell string, cols, rows int) (*TermSession, error) {
	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		return nil, err
	}
	session := &TermSession{cmd: cmd, pty: f}
	go session.readLoop(write, sessionID)
	return session, nil
}

func (t *TermSession) readLoop(write func(protocol.Message) error, sessionID string) {
	buf := make([]byte, 4096)
	for {
		n, err := t.pty.Read(buf)
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

// Input writes terminal input to the PTY.
func (t *TermSession) Input(data string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	_, err := t.pty.Write([]byte(data))
	return err
}

// Resize changes the PTY window size.
func (t *TermSession) Resize(cols, rows int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	return pty.Setsize(t.pty, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// Close kills the shell and closes the PTY.
func (t *TermSession) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	return t.pty.Close()
}
