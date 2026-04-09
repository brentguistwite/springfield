package storage

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Work is the Springfield-owned runtime boundary for one work item.
type Work struct {
	Runtime Runtime
	ID      string
}

// Work builds a validated Springfield work boundary rooted under .springfield/work/.
func (r Runtime) Work(id string) (Work, error) {
	cleanID, err := cleanWorkSegment("work id", id)
	if err != nil {
		return Work{}, err
	}

	return Work{
		Runtime: r,
		ID:      cleanID,
	}, nil
}

// WorkIndexPath returns the shared work index path.
func (r Runtime) WorkIndexPath() string {
	return filepath.Join(r.Dir, "work", "index.json")
}

// DirPath returns the per-work directory path.
func (w Work) DirPath() string {
	return filepath.Join(w.Runtime.Dir, "work", w.ID)
}

// RequestPath returns the source request markdown path for the work item.
func (w Work) RequestPath() string {
	return filepath.Join(w.DirPath(), "request.md")
}

// WorkstreamPath returns one workstream state file path.
func (w Work) WorkstreamPath(name string) string {
	return filepath.Join(w.DirPath(), "workstream-"+cleanOptionalWorkSegment(name)+".json")
}

// RunStatePath returns the aggregate run state path for the work item.
func (w Work) RunStatePath() string {
	return filepath.Join(w.DirPath(), "run-state.json")
}

func cleanWorkSegment(label, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%s must not be empty", label)
	}
	if trimmed == "." || trimmed == ".." {
		return "", fmt.Errorf("%s must not be %q", label, trimmed)
	}
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return "", fmt.Errorf("%s must be a single path segment: %s", label, value)
	}

	return trimmed, nil
}

func cleanOptionalWorkSegment(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "." || trimmed == ".." {
		return "default"
	}
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return "default"
	}

	return trimmed
}
