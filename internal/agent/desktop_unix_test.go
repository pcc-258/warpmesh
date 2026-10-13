//go:build !windows

package agent

import (
	"slices"
	"testing"
)

func TestAutoVNCIsLoopbackOnly(t *testing.T) {
	args := vncArgs(":1", 5902)
	if !slices.Contains(args, "-localhost") {
		t.Fatalf("unauthenticated VNC must bind loopback: %v", args)
	}
	port := slices.Index(args, "-rfbport")
	if port < 0 || port+1 >= len(args) || args[port+1] != "5902" {
		t.Fatalf("configured VNC port lost: %v", args)
	}
}
