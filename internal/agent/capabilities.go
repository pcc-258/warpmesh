package agent

import (
	"fmt"
	"net"
	"runtime"
	"time"

	"github.com/pcc-258/warpmesh/internal/protocol"
)

func (a *Agent) capabilities() *protocol.DeviceCapabilities {
	c := &protocol.DeviceCapabilities{Terminal: true, Files: true}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", a.cfg.VNCPort), 500*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		c.Desktop = true
		c.DesktopHint = "VNC service is reachable. Screen access still depends on the desktop session and system permissions."
		return c
	}
	switch runtime.GOOS {
	case "darwin":
		c.DesktopHint = "Enable Screen Sharing in System Settings > General > Sharing, then check screen access permissions."
	case "windows":
		c.DesktopHint = "Install and start TightVNC Server. Allow local connections to the configured VNC port."
	default:
		c.DesktopHint = "Start x11vnc in your X11 session, or use the WarpMesh desktop container for a virtual desktop. Wayland may need an X11 session."
	}
	return c
}
