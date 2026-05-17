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
// the captures. The batchID parameter is currently informational — evidence
// directories are scoped per plan key rather than per batch, but the
// project has at most one live batch at a time, so a project-wide walk
// is equivalent. The parameter is kept on the signature for forward
// compatibility with a future per-batch evidence layout.
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
	_ = batchID
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

