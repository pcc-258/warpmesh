package node

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pcc-258/warpmesh/internal/protocol"
	"github.com/pcc-258/warpmesh/internal/version"
)

const updateMaxBytes = 128 << 20

func (a *Node) handleUpdate(msg protocol.Message) {
	service := strings.TrimSpace(msg.UpdateService)
	if service == "" {
		service = strings.TrimSpace(a.cfg.UpdateService)
	}
	if service == "/usr/bin/env" || service == "env" || strings.EqualFold(service, "warpmesh-node") {
		service = ""
	}
	fail := func(state string, err error) {
		a.sendUpdateStateForService(msg.UpdateID, state, msg.UpdateVersion, err.Error(), service)
	}
	if msg.UpdateURL == "" || msg.UpdateVersion == "" || !isSHA256Hex(msg.UpdateSHA256) {
		fail("failed", errors.New("invalid update task"))
		return
	}
	if len(msg.UpdateVersion) > 128 {
		fail("failed", errors.New("update version is too long"))
		return
	}
	if msg.UpdateVersion == version.Current().Version && version.Current().Version != "dev" {
		a.sendUpdateState(msg.UpdateID, "succeeded", msg.UpdateVersion, "")
		return
	}
	canonicalURL, err := a.updateURL(msg.UpdateURL)
	if err != nil {
		fail("failed", err)
		return
	}
	if strings.HasPrefix(canonicalURL, "http://") && !loopbackRawURL(canonicalURL) {
		fail("failed", errors.New("server update URL is not HTTPS"))
		return
	}
	a.sendUpdateState(msg.UpdateID, "downloading", msg.UpdateVersion, "")
	client := &http.Client{Timeout: 10 * time.Minute}
	request, err := http.NewRequest(http.MethodGet, canonicalURL, nil)
	if err != nil {
		fail("failed", err)
		return
	}
	request.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	response, err := client.Do(request)
	if err != nil {
		fail("failed", err)
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		fail("failed", fmt.Errorf("download returned %d", response.StatusCode))
		return
	}
	stagingDir := filepath.Join(a.cfg.DataDir, "updates")
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		fail("failed", err)
		return
	}
	target := filepath.Join(stagingDir, "warpmesh-node.next")
	if runtime.GOOS == "windows" {
		target += ".exe"
	}
	file, err := os.OpenFile(target+".part", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		fail("failed", err)
		return
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, updateMaxBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > updateMaxBytes {
		_ = os.Remove(target + ".part")
		fail("failed", fmt.Errorf("download failed: %v", errors.Join(copyErr, closeErr)))
		return
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(digest, msg.UpdateSHA256) {
		_ = os.Remove(target + ".part")
		fail("failed", errors.New("sha256 mismatch"))
		return
	}
	if err := os.Rename(target+".part", target); err != nil {
		fail("failed", err)
		return
	}
	if err := os.Chmod(target, 0o700); err != nil {
		fail("failed", err)
		return
	}
	a.sendUpdateState(msg.UpdateID, "verifying", msg.UpdateVersion, "")
	check := exec.Command(target, "version")
	output, err := check.CombinedOutput()
	if err != nil || !strings.Contains(string(output), msg.UpdateVersion) {
		_ = os.Remove(target)
		fail("failed", fmt.Errorf("staged node version check failed: %v %s", err, strings.TrimSpace(string(output))))
		return
	}

	if runtime.GOOS == "windows" {
		a.sendUpdateState(msg.UpdateID, "staged", msg.UpdateVersion, "service manager restart required")
		return
	}
	executable, err := filepath.EvalSymlinks(executablePath())
	if err != nil {
		fail("failed", err)
		return
	}
	if service == "" {
		a.sendUpdateState(msg.UpdateID, "staged", msg.UpdateVersion, "restart required")
		return
	}
	if !validServiceName(service) {
		fail("failed", errors.New("invalid update service name"))
		return
	}
	if filepath.Base(executable) != "warpmesh-node" {
		a.sendUpdateState(msg.UpdateID, "staged", msg.UpdateVersion, "managed Node executable required")
		return
	}
	a.sendUpdateState(msg.UpdateID, "installing", msg.UpdateVersion, "")
	oldTarget := target + ".old"
	_ = os.Remove(oldTarget)
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, oldTarget); err != nil {
			fail("failed", fmt.Errorf("retire previous staged binary: %w", err))
			return
		}
	}
	backup := executable + ".previous"
	_ = os.Remove(backup)
	if err := os.Rename(executable, backup); err != nil {
		fail("failed", fmt.Errorf("backup current node: %w", err))
		return
	}
	if err := os.Rename(target, executable); err != nil {
		_ = os.Rename(backup, executable)
		_ = os.Rename(oldTarget, target)
		fail("failed", fmt.Errorf("install staged node: %w", err))
		return
	}
	_ = os.Remove(oldTarget)
	a.sendUpdateState(msg.UpdateID, "restarting", msg.UpdateVersion, "")
	script := `sleep 3
if ! systemctl restart "$1"; then
  mv -f "$2" "$3"
  systemctl restart "$1"
  exit 1
fi
sleep 8
if ! systemctl is-active --quiet "$1"; then
  mv -f "$2" "$3"
  systemctl restart "$1"
fi`
	child := exec.Command("sh", "-c", script, "warpmesh-update", service, backup, executable)
	detachUpdateChild(child)
	child.Stdout = nil
	child.Stderr = nil
	if err := child.Start(); err != nil {
		_ = os.Rename(executable, target)
		_ = os.Rename(backup, executable)
		fail("failed", fmt.Errorf("restart node service: %w", err))
	}
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func loopbackRawURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "http" && loopbackURL(parsed))
}

func (a *Node) updateURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "" {
		if parsed.Scheme != "https" && parsed.Scheme != "http" {
			return "", errors.New("update URL must use HTTPS")
		}
		if parsed.Scheme == "http" && !loopbackURL(parsed) {
			return "", errors.New("insecure update URL is only allowed on loopback")
		}
		return parsed.String(), nil
	}
	base := a.cfg.ServerURL
	if strings.HasPrefix(base, "ws://") || strings.HasPrefix(base, "wss://") {
		base = strings.Replace(base, "wss://", "https://", 1)
		base = strings.Replace(base, "ws://", "http://", 1)
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	baseURL.Path = strings.TrimSuffix(baseURL.Path, "/ws/node")
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + "/" + strings.TrimLeft(parsed.Path, "/")
	baseURL.RawQuery = parsed.RawQuery
	return baseURL.String(), nil
}

func executablePath() string {
	value, _ := os.Executable()
	return value
}

func loopbackURL(value *url.URL) bool {
	host := strings.ToLower(value.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (a *Node) sendUpdateState(id, state, target, detail string) {
	a.sendUpdateStateForService(id, state, target, detail, a.cfg.UpdateService)
}

func (a *Node) sendUpdateStateForService(id, state, target, detail, service string) {
	_ = a.send(protocol.Message{
		Type:          protocol.TypeNodeUpdateState,
		UpdateID:      id,
		UpdateState:   state,
		UpdateVersion: target,
		UpdateError:   detail,
		UpdateService: service,
	})
}

func validServiceName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == '@' {
			continue
		}
		return false
	}
	return true
}
