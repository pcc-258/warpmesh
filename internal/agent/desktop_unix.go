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

// ensureVNCServer provisions or guides the user toward a local VNC server.
func (a *Agent) ensureVNCServer() {
	time.Sleep(2 * time.Second)
	if vncListening(a.cfg.VNCPort) {
		return
	}
	if runtime.GOOS == "darwin" {
		log.Printf("macOS Screen Sharing is not enabled. Enable it once:")
		log.Printf("System Settings > General > Sharing > Screen Sharing")
		_ = exec.Command("open", "x-apple.systempreferences:com.apple.Sharing-Settings.extension").Start()
		return
	}
	if !hasBinary("x11vnc") {
		log.Printf("remote desktop needs x11vnc but it is not installed.")
		log.Printf("install with: apt-get install -y x11vnc  (Debian/Ubuntu)")
		log.Printf("             apk add x11vnc           (Alpine)")
		log.Printf("             dnf install -y x11vnc     (Fedora/RHEL)")
		log.Printf("or use the bundled warpmesh-desktop container image.")
		return
	}
	if os.Getenv("DISPLAY") == "" && !hasBinary("Xvfb") {
		log.Printf("headless Linux needs Xvfb for a virtual desktop.")
		log.Printf("install with: apt-get install -y xvfb x11vnc  (Debian/Ubuntu)")
		log.Printf("             apk add xvfb x11vnc              (Alpine)")
		return
	}
	if os.Getenv("DISPLAY") == "" {
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
