package retro

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// retroFileName is the on-disk name of the persisted report, a sibling of the
// archive.json Extract reads from — so a batch archive dir carries both the raw
// evidence header and the derived retrospective side by side.
const retroFileName = "retro.json"

// WriteReport persists r as <batchDir>/retro.json.
//
// The write is atomic (temp file + fsync + rename + parent-dir fsync), mirroring
// batch/storage.go's writeFileAtomic: the retrospective is the durable output of
// a whole batch's analysis, so a crash mid-write must never leave a half-written
// retro.json that a downstream consumer would parse as truth. Because the bytes
// only ever become visible at the final path via os.Rename, an observer scanning
// batchDir sees either the previous complete file or the new complete file —
// never a partial one.
//
// The rewrite is idempotent by construction: rename replaces the target
// wholesale, so re-running the classifier over the same batch overwrites the
// prior retro.json cleanly with no stale-byte tail. batchDir is not created
// here — it is the existing archive dir Extract read from — but any missing
// parent is materialized so the write does not fail on a fresh temp fixture dir.
func WriteReport(batchDir string, r *Report) error {
	if strings.TrimSpace(batchDir) == "" {
		return fmt.Errorf("retro: batchDir must not be empty")
	}
	if r == nil {
		return fmt.Errorf("retro: report must not be nil")
	}

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("retro: encode report: %w", err)
	}
	data = append(data, '\n')

	return writeFileAtomic(filepath.Join(batchDir, retroFileName), data, 0o644)
}

// writeFileAtomic writes via temp file + fsync + rename + parent dir fsync so a
// crash never leaves a partially written target AND the rename itself survives
// power loss — POSIX does not guarantee rename durability until the parent
// directory is fsynced. This mirrors batch/storage.go's helper of the same name;
// retro is a leaf package that cannot reach that unexported function, so the
// durable-write discipline is restated here rather than shared.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("retro: create parent dir for %s: %w", path, err)
	}
	// The temp file is named distinctly from the target so a crash between
	// create and rename strands a ".tmp-retro.json-*" — never a truncated
	// retro.json under the name consumers read.
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("retro: create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("retro: write temp for %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("retro: fsync temp for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("retro: close temp for %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return fmt.Errorf("retro: chmod temp for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("retro: rename temp to %s: %w", path, err)
	}
	// fsync the parent directory so the rename survives power loss. Non-fatal if
	// the platform rejects fsync on a directory (rare).
	if dirf, err := os.Open(dir); err == nil {
		_ = dirf.Sync()
		_ = dirf.Close()
	}
	return nil
}
