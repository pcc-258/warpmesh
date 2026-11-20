//go:build !windows

package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

func runClient(options clientOptions) {
	if !waitForNode(options) {
		fmt.Println("Local WarpMesh node UI is not available at", options.nodeURL)
	}
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", options.nodeURL).Start()
	default:
		_ = exec.Command("xdg-open", options.nodeURL).Start()
	}
}
