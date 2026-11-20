//go:build !unix

package node

import "os/exec"

func detachUpdateChild(command *exec.Cmd) {
	_ = command
}
