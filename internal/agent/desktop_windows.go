//go:build windows

package agent

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ensureVNCServer starts a bundled VNC server when one ships with the agent.
func (a *Agent) ensureVNCServer() {
	time.Sleep(2 * time.Second)
	if vncListeningWindows(a.cfg.VNCPort) {
		return
	}
	exe, _ := os.Executable()
	server := filepath.Join(filepath.Dir(exe), "tvnserver.exe")
	if _, err := os.Stat(server); err != nil {
		log.Printf("bundled VNC server not found next to agent (%s); remote desktop needs a VNC server on 127.0.0.1:%d", server, a.cfg.VNCPort)
		return
	}
	_ = exec.Command(server, "-start").Start()
	log.Printf("started bundled TightVNC server on port %d", a.cfg.VNCPort)
}

func vncListeningWindows(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
