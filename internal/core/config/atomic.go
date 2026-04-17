package config

import (
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path via a temp file + rename so a failed
// write never leaves path empty or partially written. On POSIX, rename on the
// same filesystem is atomic.
//
// perm is applied via fchmod on the open file descriptor before close, so the
// temp file is already at its final permissions at the moment of rename. This
// avoids a path-based race that would exist with a post-close os.Chmod(path, …).
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	closed := false
	defer func() {
		if !closed {
			// Second Close is a no-op because we discard the error; we only reach
			// this branch on an early return before the explicit Close below.
			_ = f.Close()
		}
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Chmod(perm); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	closed = true
	return os.Rename(tmpPath, path)
}
