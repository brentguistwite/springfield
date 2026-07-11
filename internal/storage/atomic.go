package storage

import (
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path via a temp file + rename so a concurrent
// lockless reader (e.g. `springfield status`) never observes a partial or empty
// file mid-write. On POSIX, rename on the same filesystem is atomic: a reader
// sees either the old bytes or the new bytes, never a truncated in-between.
//
// perm is applied via fchmod on the open descriptor before close, so the temp
// file is already at its final permissions at the moment of rename — avoiding a
// path-based race a post-close os.Chmod(path, …) would introduce. Mirrors
// core/config.writeFileAtomic; kept package-local so storage does not depend on
// config.
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
