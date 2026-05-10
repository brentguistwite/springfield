package planrun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"springfield/internal/features/prd"
)

// MarkPassed marks a user story as passed in prd.json (atomic temp+rename).
// Idempotent: re-marking an already-passed story is a no-op (returns nil
// without rewriting the file).
// Errors are FATAL — caller must abort the iteration.
// Refuses with error if storyID is not present in the PRD.
func MarkPassed(prdPath string, storyID string) error {
	p, err := prd.ParseFile(prdPath)
	if err != nil {
		return fmt.Errorf("MarkPassed: load %s: %w", prdPath, err)
	}

	idx := -1
	for i, s := range p.UserStories {
		if s.ID == storyID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("MarkPassed: story %q not found in %s", storyID, prdPath)
	}

	if p.UserStories[idx].Passes {
		// Already passed — idempotent no-op, no rewrite.
		return nil
	}

	p.UserStories[idx].Passes = true

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("MarkPassed: marshal: %w", err)
	}
	data = append(data, '\n')

	if err := writeFileAtomicPlanrun(prdPath, data, 0o644); err != nil {
		return fmt.Errorf("MarkPassed: write %s: %w", prdPath, err)
	}
	return nil
}

// AppendProgress appends a single line entry to progress.md. The runner is
// the sole writer; the agent never invokes this and is forbidden from
// writing the file.
// Errors are NON-FATAL — caller should log and continue. The chronological
// log is debugging aid, not authoritative state (prd.json + state.json are).
func AppendProgress(progressPath string, entry string) error {
	line := strings.TrimRight(entry, "\n") + "\n"
	f, err := os.OpenFile(progressPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("AppendProgress: open %s: %w", progressPath, err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("AppendProgress: write %s: %w", progressPath, err)
	}
	return nil
}

// writeFileAtomicPlanrun writes via temp file + fsync + rename so a crash
// never leaves a partially written target. Same contract as batch.writeFileAtomic;
// copied here to avoid cross-package dependency on an unexported helper.
func writeFileAtomicPlanrun(path string, data []byte, perm os.FileMode) error {
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
	if dirf, err := os.Open(dir); err == nil {
		_ = dirf.Sync()
		_ = dirf.Close()
	}
	return nil
}
