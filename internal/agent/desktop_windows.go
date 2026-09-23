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

// ensureVNCServer starts TightVNC when present, otherwise guides install.
func (a *Agent) ensureVNCServer() {
	time.Sleep(2 * time.Second)
	if vncListeningWindows(a.cfg.VNCPort) {
		return
	}
	exe, _ := os.Executable()
	candidates := []string{
		filepath.Join(filepath.Dir(exe), "tvnserver.exe"),
		`C:\Program Files\TightVNC\tvnserver.exe`,
		`C:\Program Files (x86)\TightVNC\tvnserver.exe`,
	}
	var server string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			server = c
			break
		}
	}
	if server == "" {
		log.Printf("remote desktop needs TightVNC Server but it is not installed.")
		log.Printf("run: warpmesh-agent vnc-install   (downloads the installer to data dir)")
		log.Printf("or install TightVNC from https://www.tightvnc.com and allow localhost connections on port %d", a.cfg.VNCPort)
		return
	}
	_ = exec.Command(server, "-start").Start()
	log.Printf("started TightVNC server on port %d", a.cfg.VNCPort)
}

func vncListeningWindows(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
