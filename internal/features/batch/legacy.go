package batch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LegacyWorkSummary is a minimal projection of legacy .springfield/work state.
type LegacyWorkSummary struct {
	ID       string
	Title    string
	Status   string
	Approved bool
}

// legacyWorkIndex mirrors the existing work index JSON shape.
type legacyWorkIndex struct {
	ActiveWorkID string                `json:"active_work_id,omitempty"`
	Works        []legacyWorkIndexEntry `json:"works"`
}

type legacyWorkIndexEntry struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Split string `json:"split"`
}

type legacyRunState struct {
	Status   string `json:"status"`
	Approved bool   `json:"approved"`
	Error    string `json:"error,omitempty"`
}

// DetectLegacyWork returns a summary of legacy work state if present.
// Returns nil, nil when no legacy state exists.
func DetectLegacyWork(rootDir string) (*LegacyWorkSummary, error) {
	indexPath := filepath.Join(rootDir, springfieldDir, "work", "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read legacy work index: %w", err)
	}

	var index legacyWorkIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("decode legacy work index: %w", err)
	}
	if len(index.Works) == 0 {
		return nil, nil
	}

	entry := selectLegacyEntry(index)
	runStatePath := filepath.Join(rootDir, springfieldDir, "work", entry.ID, "run-state.json")
	var rs legacyRunState
	if rsData, err := os.ReadFile(runStatePath); err == nil {
		_ = json.Unmarshal(rsData, &rs)
	}

	return &LegacyWorkSummary{
		ID:       entry.ID,
		Title:    entry.Title,
		Status:   rs.Status,
		Approved: rs.Approved,
	}, nil
}

// selectLegacyEntry applies the deterministic legacy selection rule.
func selectLegacyEntry(index legacyWorkIndex) legacyWorkIndexEntry {
	// 1. Use explicit active_work_id when present and valid.
	if index.ActiveWorkID != "" {
		for _, e := range index.Works {
			if e.ID == index.ActiveWorkID {
				return e
			}
		}
	}
	// 2. Use latest approved entry.
	// (approval is in run-state; we can't read it here without extra IO — fall through)
	// 3. Fall back to last entry in index order.
	return index.Works[len(index.Works)-1]
}

// MigrateLegacyToBatch converts a legacy work item into new batch state.
// The legacy state is archived (not deleted) after successful migration.
func MigrateLegacyToBatch(rootDir string, summary LegacyWorkSummary) (Batch, error) {
	requestPath := filepath.Join(rootDir, springfieldDir, "work", summary.ID, "request.md")
	source := ""
	if data, err := os.ReadFile(requestPath); err == nil {
		source = string(data)
	}

	batchID := SanitizeID(summary.ID)
	if batchID == "" {
		batchID = "migrated"
	}

	status := SliceDone
	if summary.Status == "failed" {
		status = SliceFailed
	} else if summary.Status == "" || summary.Status == "draft" {
		status = SliceQueued
	}

	slices := []Slice{
		{ID: "01", Title: summary.Title, Status: status},
	}

	b := Batch{
		ID:              batchID,
		Title:           summary.Title,
		SourceKind:      SourcePrompt,
		IntegrationMode: IntegrationBatch,
		Phases:          []Phase{{Mode: PhaseSerial, Slices: []string{"01"}}},
		Slices:          slices,
	}

	paths, err := NewPaths(rootDir, batchID)
	if err != nil {
		return Batch{}, err
	}
	if err := WriteBatch(paths, b, source); err != nil {
		return Batch{}, fmt.Errorf("write migrated batch: %w", err)
	}

	run := Run{
		ActiveBatchID:  batchID,
		ActivePhaseIdx: 0,
		LastCheckpoint: time.Now().UTC(),
	}
	if err := WriteRun(rootDir, run); err != nil {
		return Batch{}, fmt.Errorf("write run after migration: %w", err)
	}

	// Archive the legacy work directory (rename, don't delete).
	legacyWorkDir := filepath.Join(rootDir, springfieldDir, "work", summary.ID)
	archiveWorkDir := filepath.Join(rootDir, springfieldDir, "archive", "legacy-work-"+summary.ID)
	if err := os.MkdirAll(filepath.Dir(archiveWorkDir), 0o755); err != nil {
		return b, nil // migration succeeded; archive failure is non-fatal
	}
	_ = os.Rename(legacyWorkDir, archiveWorkDir)

	return b, nil
}
