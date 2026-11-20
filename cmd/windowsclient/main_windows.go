//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

const loadingPage = `<!doctype html>
<html><head><meta charset="utf-8"><title>WarpMesh Client</title>
<style>
body{margin:0;background:#0b0e12;color:#edf3f7;font:14px system-ui,sans-serif;display:grid;place-items:center;height:100vh}
main{text-align:center;max-width:460px;padding:30px}
.mark{width:52px;height:52px;border-radius:9px;background:#39d5b0;color:#06251e;display:grid;place-items:center;margin:0 auto 20px;font-size:24px;font-weight:900}
h1{font-size:21px;margin:0 0 9px} p{color:#91a0af;line-height:1.6;margin:0}
</style></head><body><main><div class="mark">W</div><h1>Starting WarpMesh Client</h1>
<p>Connecting to the local node service...</p></main></body></html>`

const unavailablePage = `<!doctype html>
<html><head><meta charset="utf-8"><title>WarpMesh Client</title>
<style>
body{margin:0;background:#0b0e12;color:#edf3f7;font:14px system-ui,sans-serif;display:grid;place-items:center;height:100vh}
main{max-width:560px;padding:34px;border:1px solid #28323e;border-radius:9px;background:#131820}
.mark{width:48px;height:48px;border-radius:8px;background:#f4b860;color:#201707;display:grid;place-items:center;margin-bottom:18px;font-size:22px;font-weight:900}
h1{font-size:21px;margin:0 0 9px} p{color:#91a0af;line-height:1.65;margin:0 0 12px} code{color:#8de7d0}
</style></head><body><main><div class="mark">!</div><h1>Local node is not available</h1>
<p>WarpMesh Client only connects to a loopback node UI. Start the WarpMesh node service, then reopen this client.</p>
<p>Expected endpoint: <code>http://127.0.0.1:9876</code></p></main></body></html>`

func runClient(options clientOptions) {
	appData, err := os.UserCacheDir()
	if err != nil {
		appData = os.TempDir()
	}
	dataPath := filepath.Join(appData, "WarpMesh", "WebView2")
	_ = os.MkdirAll(dataPath, 0o700)
	view := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     options.debug,
		DataPath:  dataPath,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "WarpMesh Client",
			Width:  1280,
			Height: 820,
			Center: true,
		},
	})
	if view == nil {
		openSystemBrowser(options.nodeURL)
		return
	}
	defer view.Destroy()
	view.SetSize(960, 640, webview2.HintMin)
	view.SetHtml(loadingPage)
	go func() {
		if waitForNode(options) {
			view.Dispatch(func() { view.Navigate(options.nodeURL) })
			return
		}
		html := strings.Replace(unavailablePage, "http://127.0.0.1:9876", options.nodeURL, 1)
		view.Dispatch(func() { view.SetHtml(html) })
	}()
	view.Run()
}

func openSystemBrowser(target string) {
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start(); err == nil {
		return
	}
	message, _ := windows.UTF16PtrFromString(fmt.Sprintf(
		"Microsoft Edge WebView2 Runtime is required.\n\nInstall it, then reopen WarpMesh Client.\n\nLocal UI: %s",
		target,
	))
	title, _ := windows.UTF16PtrFromString("WarpMesh Client")
	dll := windows.NewLazySystemDLL("user32.dll")
	messageBox := dll.NewProc("MessageBoxW")
	_, _, _ = messageBox.Call(
		0,
		uintptr(unsafe.Pointer(message)),
		uintptr(unsafe.Pointer(title)),
		windows.MB_OK|windows.MB_ICONERROR,
	)
}
