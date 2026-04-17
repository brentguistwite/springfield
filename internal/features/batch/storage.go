package batch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// WriteBatch persists the compiled batch and source to disk.
func WriteBatch(paths Paths, b Batch, source string) error {
	if err := os.MkdirAll(paths.PlanDir(), 0o755); err != nil {
		return fmt.Errorf("create plan dir: %w", err)
	}

	if err := os.WriteFile(paths.SourcePath(), []byte(source), 0o644); err != nil {
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
func ArchiveBatch(rootDir string, b Batch, reason string) error {
	slug := time.Now().UTC().Format("20060102T150405Z")
	archivePath := ArchivePath(rootDir, slug, b.ID)

	slices := make([]ArchiveSlice, 0, len(b.Slices))
	for _, s := range b.Slices {
		slices = append(slices, ArchiveSlice{ID: s.ID, Title: s.Title, Status: s.Status})
	}

	entry := ArchiveEntry{
		BatchID:    b.ID,
		Title:      b.Title,
		ArchivedAt: time.Now().UTC(),
		Reason:     reason,
		Slices:     slices,
	}

	if err := os.MkdirAll(ArchiveDir(rootDir), 0o755); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}
	if err := writeJSON(archivePath, entry); err != nil {
		return err
	}

	paths, err := NewPaths(rootDir, b.ID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(paths.PlanDir()); err != nil {
		return fmt.Errorf("remove plan dir: %w", err)
	}

	return nil
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

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent dir for %s: %w", path, err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode json %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write json %s: %w", path, err)
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
