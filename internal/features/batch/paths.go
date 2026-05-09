package batch

import (
	"fmt"
	"path/filepath"
	"strings"
)

const springfieldDir = ".springfield"

// Paths resolves state file paths for one batch under .springfield.
type Paths struct {
	rootDir string
	batchID string
}

// NewPaths returns a Paths resolver for the given project root and batch id.
func NewPaths(rootDir, batchID string) (Paths, error) {
	if strings.TrimSpace(batchID) == "" {
		return Paths{}, fmt.Errorf("batch id must not be empty")
	}
	if strings.ContainsAny(batchID, `/\`) {
		return Paths{}, fmt.Errorf("batch id must be a single path segment: %s", batchID)
	}
	return Paths{rootDir: rootDir, batchID: batchID}, nil
}

// PlanDir returns the batch plan directory.
func (p Paths) PlanDir() string {
	return filepath.Join(p.rootDir, springfieldDir, "plans", p.batchID)
}

// SourcePath returns the source markdown path.
func (p Paths) SourcePath() string {
	return filepath.Join(p.PlanDir(), "source.md")
}

// BatchPath returns the compiled batch JSON path.
func (p Paths) BatchPath() string {
	return filepath.Join(p.PlanDir(), "batch.json")
}

// EvidenceDir returns the per-plan evidence directory under the batch plan dir.
// Evidence is keyed by plan within a batch. Phase 4 adds iter-N subdirs under it.
func (p Paths) EvidenceDir(planID string) string {
	return filepath.Join(p.PlanDir(), "evidence", planID)
}

// PlanDirByID returns the per-plan directory: <root>/.springfield/plans/<planID>.
// Distinct from PlanDir (which uses batchID) — this is the per-plan-id dir.
func PlanDirByID(rootDir, planID string) string {
	return filepath.Join(rootDir, springfieldDir, "plans", planID)
}

// PlanPRDPath returns the canonical prd.json path for a plan:
// <root>/.springfield/plans/<planID>/prd.json
func PlanPRDPath(rootDir, planID string) string {
	return filepath.Join(PlanDirByID(rootDir, planID), "prd.json")
}

// PlanContextPath returns the context.md path for a plan:
// <root>/.springfield/plans/<planID>/context.md
func PlanContextPath(rootDir, planID string) string {
	return filepath.Join(PlanDirByID(rootDir, planID), "context.md")
}

// PlanProgressPath returns the progress.md path for a plan:
// <root>/.springfield/plans/<planID>/progress.md
func PlanProgressPath(rootDir, planID string) string {
	return filepath.Join(PlanDirByID(rootDir, planID), "progress.md")
}

// RunPath returns the active runtime cursor path (shared across batches).
func RunPath(rootDir string) string {
	return filepath.Join(rootDir, springfieldDir, "run.json")
}

// ArchiveDir returns the archive directory path.
func ArchiveDir(rootDir string) string {
	return filepath.Join(rootDir, springfieldDir, "archive")
}

// StableArchivePath returns the canonical archive file path for a batch id.
// One path per batch id means concurrent archive attempts race at O_EXCL
// create time, making "exactly one archive per batch" a filesystem invariant
// rather than a hope.
func StableArchivePath(rootDir, batchID string) string {
	return filepath.Join(ArchiveDir(rootDir), batchID+".json")
}
