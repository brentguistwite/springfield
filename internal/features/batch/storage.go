package batch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// WriteBatch persists the compiled batch and source to disk.
func WriteBatch(paths Paths, b Batch, source string) error {
	if err := os.MkdirAll(paths.PlanDir(), 0o755); err != nil {
		return fmt.Errorf("create plan dir: %w", err)
	}

	if err := writeFileAtomic(paths.SourcePath(), []byte(source), 0o644); err != nil {
		return fmt.Errorf("write source: %w", err)
	}

	return writeJSON(paths.BatchPath(), b)
}

// ReadBatch reads the compiled batch for the given batch id.
func ReadBatch(paths Paths) (Batch, error) {
	var b Batch
	if err := readJSON(paths.BatchPath(), &b); err != nil {
		return Batch{}, fmt.Errorf("read batch %s: %w", paths.batchID, err)
	}
	return b, nil
}

// ReadBatchBytes returns the raw bytes of batch.json for snapshotting.
func ReadBatchBytes(paths Paths) ([]byte, error) {
	return os.ReadFile(paths.BatchPath())
}

// WriteRun persists the runtime cursor state.
func WriteRun(rootDir string, r Run) error {
	return writeJSON(RunPath(rootDir), r)
}

// ReadRun reads the runtime cursor state. Returns zero Run if file does not exist.
func ReadRun(rootDir string) (Run, bool, error) {
	var r Run
	path := RunPath(rootDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Run{}, false, nil
		}
		return Run{}, false, fmt.Errorf("read run.json: %w", err)
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return Run{}, false, fmt.Errorf("decode run.json: %w", err)
	}
	return r, true, nil
}

// ClearRun removes the runtime cursor state.
func ClearRun(rootDir string) error {
	err := os.Remove(RunPath(rootDir))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear run.json: %w", err)
	}
	return nil
}

// ArchiveBatch writes a compact archive entry and removes the plan dir.
//
// Deprecated: prefer ArchiveBatchNormalized, which normalizes non-terminal slice
// statuses to SliceAborted so archives never record a "running" slice alongside
// a terminal reason.
func ArchiveBatch(rootDir string, b Batch, reason string) error {
	return ArchiveBatchNormalized(rootDir, b, reason)
}

// ArchiveBatchNormalized rewrites any non-terminal slice status to SliceAborted,
// then atomically writes the archive entry (exactly once per batch id) and
// removes the plan directory.
//
// Single-writer contract: the archive path is a stable per-batch id
// (StableArchivePath); the archive entry is created with O_EXCL so two
// concurrent writers cannot both succeed. If the archive already exists for
// this batch id, the call is idempotent: plan dir is removed, no duplicate
// entry is written.
//
// Durability contract: the archive bytes are fsynced, the rename makes them
// visible atomically, and the parent directory is fsynced so the rename
// survives power loss. Only after that is the live plan dir removed.
func ArchiveBatchNormalized(rootDir string, b Batch, reason string) error {
	archivePath := StableArchivePath(rootDir, b.ID)

	if err := os.MkdirAll(ArchiveDir(rootDir), 0o755); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}

	slices := make([]ArchiveSlice, 0, len(b.Slices))
	for _, s := range b.Slices {
		status := s.Status
		if !status.IsTerminal() {
			status = SliceAborted
		}
		slices = append(slices, ArchiveSlice{ID: s.ID, Title: s.Title, Status: status})
	}

	entry := ArchiveEntry{
		BatchID:    b.ID,
		Title:      b.Title,
		ArchivedAt: time.Now().UTC(),
		Reason:     reason,
		Slices:     slices,
	}

	wrote, err := writeJSONExclusive(archivePath, entry)
	if err != nil {
		return err
	}
	_ = wrote // idempotent either way — continue to plan dir cleanup

	paths, err := NewPaths(rootDir, b.ID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(paths.PlanDir()); err != nil {
		return fmt.Errorf("remove plan dir (archive at %s): %w", archivePath, err)
	}

	return nil
}

// RecoverOrphan writes a stub archive entry for a run whose batch directory
// has vanished, then clears run.json. Idempotent across concurrent callers:
// the archive is created with O_EXCL at a stable per-batch path, so at most
// one orphan archive lands per batch id.
func RecoverOrphan(rootDir string, run Run) error {
	if run.ActiveBatchID == "" {
		return ClearRun(rootDir)
	}

	if err := os.MkdirAll(ArchiveDir(rootDir), 0o755); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}
	archivePath := StableArchivePath(rootDir, run.ActiveBatchID)
	entry := ArchiveEntry{
		BatchID:    run.ActiveBatchID,
		Title:      run.ActiveBatchID,
		ArchivedAt: time.Now().UTC(),
		Reason:     "orphaned",
	}
	if _, err := writeJSONExclusive(archivePath, entry); err != nil {
		return err
	}

	// Best-effort: tidy up any residual plan dir.
	if paths, err := NewPaths(rootDir, run.ActiveBatchID); err == nil {
		_ = os.RemoveAll(paths.PlanDir())
	}

	return ClearRun(rootDir)
}

// IsMissingBatchError reports whether an error chain represents a missing
// batch.json file (a run.json pointing at a vanished batch). Works through
// ReadBatch's fmt.Errorf %w wrapping.
func IsMissingBatchError(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

// UpdateBatchSlice reads the batch, updates one slice, and writes it back.
func UpdateBatchSlice(paths Paths, updated Slice) error {
	b, err := ReadBatch(paths)
	if err != nil {
		return err
	}
	b.UpdateSlice(updated)
	return writeJSON(paths.BatchPath(), b)
}

// RestoreBatchFromSnapshot re-creates the plan directory (if missing) and
// writes the snapshot bytes back to batch.json atomically.
func RestoreBatchFromSnapshot(paths Paths, snapshot []byte) error {
	if err := os.MkdirAll(paths.PlanDir(), 0o755); err != nil {
		return fmt.Errorf("recreate plan dir: %w", err)
	}
	return writeFileAtomic(paths.BatchPath(), snapshot, 0o644)
}

func writeJSON(path string, value any) error {
	data, err := marshalJSONLine(value, path)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o644)
}

// writeJSONExclusive is writeJSON but the rename target is created via
// O_CREATE|O_EXCL: if another writer has already created it, the caller
// observes os.ErrExist via the returned error OR (when existed == true)
// skips silently for idempotent archive flows.
//
// existed == true means an archive for this path already exists on disk. The
// current call is a no-op; the caller should still proceed with follow-up
// cleanup (plan dir removal, run.json clear) so concurrent recovery is safe.
func writeJSONExclusive(path string, value any) (existed bool, err error) {
	data, err := marshalJSONLine(value, path)
	if err != nil {
		return false, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create parent dir for %s: %w", path, err)
	}

	// Attempt exclusive create at the final path first. If another process
	// has already committed an archive for this key, bail as a no-op.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return true, nil
		}
		return false, fmt.Errorf("exclusive create %s: %w", path, err)
	}
	// We hold the exclusive file. Write into a sibling tmp then rename onto
	// the created path to keep the contents atomic. The earlier exclusive
	// create is the lock; the rename makes the bytes visible atomically.
	if err := f.Close(); err != nil {
		return false, fmt.Errorf("close lock handle %s: %w", path, err)
	}
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		return false, err
	}
	return false, nil
}

func marshalJSONLine(value any, path string) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode json %s: %w", path, err)
	}
	return append(data, '\n'), nil
}

// writeFileAtomic writes via temp file + fsync + rename + parent dir fsync so
// a crash never leaves a partially written target AND the rename itself
// survives power loss. POSIX does not guarantee rename durability until the
// parent directory is fsynced.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create parent dir for %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("write temp for %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("fsync temp for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename temp to %s: %w", path, err)
	}
	// fsync the parent directory so the rename survives power loss.
	// Non-fatal if the platform rejects fsync on a directory (rare).
	if dirf, err := os.Open(dir); err == nil {
		_ = dirf.Sync()
		_ = dirf.Close()
	}
	return nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
