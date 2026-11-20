package node

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const UITokenFilename = "ui-token"

// LoadOrCreateUIToken returns the bearer token used by the local automation
// API. The token is created with owner-only permissions and never logged.
func LoadOrCreateUIToken(dataDir string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", errors.New("data directory is required for the local automation token")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dataDir, UITokenFilename)
	if raw, err := os.ReadFile(path); err == nil {
		token := strings.TrimSpace(string(raw))
		if len(token) < 32 {
			return "", fmt.Errorf("%s is too short; remove it to generate a new token", path)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return "", err
		}
		return token, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return LoadOrCreateUIToken(dataDir)
		}
		return "", err
	}
	if _, err := file.WriteString(token + "\n"); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return token, nil
}

// UITokenPath returns the conventional automation token path for a data dir.
func UITokenPath(dataDir string) string {
	return filepath.Join(dataDir, UITokenFilename)
}
