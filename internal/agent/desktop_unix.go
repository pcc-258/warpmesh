//go:build !windows

package agent

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// ensureVNCServer makes remote desktop self-contained where the OS allows it.
func (a *Agent) ensureVNCServer() {
	time.Sleep(2 * time.Second)
	if vncListening(a.cfg.VNCPort) {
		return
	}
	if runtime.GOOS == "darwin" {
		log.Printf("macOS Screen Sharing is not enabled; enable it once in System Settings > General > Sharing > Screen Sharing, then VNC port %d will be ready", a.cfg.VNCPort)
		return
	}
	if hasBinary("x11vnc") {
		if os.Getenv("DISPLAY") == "" && hasBinary("Xvfb") {
			_ = exec.Command("Xvfb", ":1", "-screen", "0", "1280x800x24").Start()
			time.Sleep(time.Second)
			_ = os.Setenv("DISPLAY", ":1")
		}
		cmd := exec.Command("x11vnc", "-display", os.Getenv("DISPLAY"), "-forever", "-shared",
			"-rfbport", fmt.Sprintf("%d", a.cfg.VNCPort), "-nopw", "-bg")
		if err := cmd.Start(); err == nil {
			log.Printf("started x11vnc on port %d", a.cfg.VNCPort)
		} else {
			log.Printf("start x11vnc failed: %v", err)
		}
		return
	}
	log.Printf("x11vnc not found; install x11vnc or use the warpmesh-desktop container image")
}

func vncListening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
