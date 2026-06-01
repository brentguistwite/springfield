package cost

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Rollup aggregates per-iteration cost captures into a single batch-level
// summary suitable for human display and cap enforcement.
type Rollup struct {
	TotalUSD     float64            `json:"total_usd"`
	PerAdapter   map[string]float64 `json:"per_adapter,omitempty"`
	Iterations   int                `json:"iterations"`
	UnpricedRuns int                `json:"unpriced_runs,omitempty"`
	// SkippedFiles counts cost.json files that existed on disk but could
	// not be read or decoded. Callers using Rollup for safety logic
	// (--cost-cap enforcement, resume guard) MUST surface a non-zero
	// value to the operator so they know the rollup may under-count.
	SkippedFiles int `json:"skipped_files,omitempty"`
}

// ComputeRollup walks the live evidence directories under
// <root>/.springfield/execution/plans/*/evidence/iter-*/cost.json and sums
// the captures whose Capture.BatchID matches batchID. Scoping by the stamped
// batch id (not by plan-key path) is required because the conductor reuses
// plan IDs across batches with iteration counters restarting at 1: a stale
// iter-N cost.json from an earlier batch can survive best-effort archive
// cleanup into a reused plan dir, and a path-based walk would silently fold
// that leaked spend into the current batch's total. A non-empty batchID
// therefore counts only captures stamped with it. An empty batchID disables
// the filter and counts every capture (the unscoped single-plan-unit path,
// which runs outside a named batch).
//
// Iterations whose pricing pair was unknown (CostUSD == 0 with non-zero
// tokens) are counted under UnpricedRuns so callers can surface a "(N
// unpriced)" hint to the operator. A capture with zero CostUSD AND zero
// tokens is treated as a real $0 iteration (covers gemini placeholders
// and noop runs), not unpriced.
//
// A missing evidence root returns an empty Rollup with no error so callers
// (status command, warning helper) need not special-case fresh projects.
func ComputeRollup(root, batchID string) (Rollup, error) {
	r := Rollup{PerAdapter: map[string]float64{}}

	execRoot := filepath.Join(root, ".springfield", "execution", "plans")
	if _, err := os.Stat(execRoot); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return r, nil
		}
		return r, err
	}

	walkErr := filepath.WalkDir(execRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// WalkDir surfaces both directory-traversal errors and per-file
			// errors here. For directory errors, skip the subtree so we
			// don't get spurious per-child errors; count this as one
			// skipped "entry" which may represent N missed files. The
			// surfaced count is therefore a lower bound — operators should
			// investigate any non-zero SkippedFiles, not assume it's exact.
			r.SkippedFiles++
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || filepath.Base(path) != "cost.json" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			r.SkippedFiles++
			return nil
		}
		var c Capture
		if jsonErr := json.Unmarshal(data, &c); jsonErr != nil {
			r.SkippedFiles++
			return nil
		}
		// Scope to the requested batch. A non-empty batchID counts only
		// captures stamped with it, excluding leaked iter-N files from an
		// earlier batch that reused this plan dir. An empty batchID counts
		// everything (unscoped single-plan-unit path). Not a SkippedFiles
		// case: the file is readable, it just belongs to a different batch.
		if batchID != "" && c.BatchID != batchID {
			return nil
		}
		r.Iterations++
		r.TotalUSD += c.CostUSD
		r.PerAdapter[c.Adapter] += c.CostUSD
		if c.CostUSD == 0 && (c.InputTokens > 0 || c.OutputTokens > 0) {
			r.UnpricedRuns++
		}
		return nil
	})
	if walkErr != nil {
		return r, walkErr
	}
	return r, nil
}
