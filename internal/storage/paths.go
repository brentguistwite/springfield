package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func newRuntime(rootDir string) (Runtime, error) {
	absRootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return Runtime{}, fmt.Errorf("resolve runtime root %q: %w", rootDir, err)
	}

	return Runtime{
		RootDir: absRootDir,
		Dir:     filepath.Join(absRootDir, DirName),
	}, nil
}

// Path resolves a runtime-relative path under .springfield.
func (r Runtime) Path(parts ...string) (string, error) {
	if len(parts) == 0 {
		return r.Dir, nil
	}

	relativePath := filepath.Join(parts...)
	if filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("runtime path must be relative: %s", relativePath)
	}

	cleanPath := filepath.Clean(relativePath)
	if cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("runtime path escapes %s: %s", DirName, relativePath)
	}

	return filepath.Join(r.Dir, cleanPath), nil
}

// Ensure creates the runtime root directory when missing.
func (r Runtime) Ensure() error {
	if err := os.MkdirAll(r.Dir, 0o755); err != nil {
		return fmt.Errorf("create runtime dir %s: %w", r.Dir, err)
	}

	return nil
}

func (r Runtime) ensureParent(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create runtime parent dir for %s: %w", path, err)
	}

	return nil
}
