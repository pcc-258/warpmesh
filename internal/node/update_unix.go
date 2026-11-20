//go:build unix

package node

import (
	"os/exec"
	"syscall"
)

func detachUpdateChild(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
