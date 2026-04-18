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

// RunPath returns the active runtime cursor path (shared across batches).
func RunPath(rootDir string) string {
	return filepath.Join(rootDir, springfieldDir, "run.json")
}

// ArchiveDir returns the archive directory path.
func ArchiveDir(rootDir string) string {
	return filepath.Join(rootDir, springfieldDir, "archive")
}

// ArchivePath returns the archive file path for a given timestamp slug and batch id.
//
// Deprecated: kept for backwards compatibility with tests/callers that still
// pass a timestamp slug. New code uses StableArchivePath so archive writes can
// be single-writer via O_EXCL without a TOCTOU race against parallel writers.
func ArchivePath(rootDir, timestampSlug, batchID string) string {
	return filepath.Join(ArchiveDir(rootDir), timestampSlug+"-"+batchID+".json")
}

// StableArchivePath returns the canonical archive file path for a batch id.
// One path per batch id means concurrent archive attempts race at O_EXCL
// create time, making "exactly one archive per batch" a filesystem invariant
// rather than a hope.
func StableArchivePath(rootDir, batchID string) string {
	return filepath.Join(ArchiveDir(rootDir), batchID+".json")
}
