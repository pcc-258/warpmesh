//go:build windows

package agent

import (
	"errors"

	"github.com/pcc-258/device-relay/internal/protocol"
)

// TermSession is a placeholder on Windows until a native PTY backend lands.
type TermSession struct{}

func startTerminalSession(_ func(protocol.Message) error, _, _ string, _, _ int) (*TermSession, error) {
	return nil, errors.New("remote terminal is not supported on Windows yet")
}

func (t *TermSession) Input(_ string) error      { return nil }
func (t *TermSession) Resize(_, _ int) error     { return nil }
func (t *TermSession) Close() error              { return nil }
