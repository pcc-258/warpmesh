package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

// fileRoot anchors all filesystem traversal to an allowed directory. Unlike a
// check followed by os.Open/Create, os.Root also confines symlink resolution
// during the actual operation, including concurrent directory/link replacement.
func (a *Agent) fileRoot(path string) (*os.Root, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	roots := a.cfg.AllowPaths
	if len(roots) == 0 {
		roots = []string{filepath.VolumeName(abs) + string(filepath.Separator)}
	}
	// Canonicalize aliases (including absolute symlinks into allowed roots)
	// for compatibility. This is NOT a check followed by an unrestricted open:
	// the resulting relative path is still opened through os.Root, so a link
	// or directory replaced after resolution cannot escape the selected root.
	candidate := resolvePath(abs)
	for _, configured := range roots {
		base, err := filepath.Abs(configured)
		if err != nil {
			continue
		}
		base = resolvePath(base)
		rel, err := filepath.Rel(base, candidate)
		if err != nil || (rel != "." && !filepath.IsLocal(rel)) {
			continue
		}
		root, err := os.OpenRoot(base)
		if err != nil {
			return nil, "", err
		}
		return root, rel, nil
	}
	return nil, "", fmt.Errorf("path is outside the allowed directories")
}

func (a *Agent) openUpload(dir, name string) (*os.File, error) {
	if name == "." || name == string(filepath.Separator) || !filepath.IsLocal(name) {
		return nil, fmt.Errorf("invalid upload name")
	}
	root, rel, err := a.fileRoot(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
		return nil, err
	}
	return root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
}
