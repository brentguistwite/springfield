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
func ArchivePath(rootDir, timestampSlug, batchID string) string {
	return filepath.Join(ArchiveDir(rootDir), timestampSlug+"-"+batchID+".json")
}
