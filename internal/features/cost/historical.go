package cost

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// archiveEntry mirrors the subset of batch.ArchiveEntry needed for the
// historical estimate. Defined locally so this package does not depend on
// batch (avoiding a cycle — batch depends on cost for the Rollup type that
// archive uses).
type archiveEntry struct {
	BatchID  string  `json:"batch_id"`
	TotalUSD float64 `json:"total_usd"`
	// ArchivedAt is a pointer so an absent field (legacy archives) and an
	// empty-string field (hand-edited or partial writes) both decode without
	// dropping the entire entry. A non-pointer time.Time would fail JSON
	// unmarshal on "" and silently exclude an otherwise-valid archive from
	// the historical estimate.
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	// Plans count is derived from the embedded plans array; only its length
	// is needed for the estimate.
	Plans []json.RawMessage `json:"plans"`
}

// EstimatePerPlanUSD scans archive entries under <root>/.springfield/archive,
// keeps the most recent `lookback` entries whose TotalUSD > 0 (legacy
// pre-cost-capture archives with zero TotalUSD are skipped — they signal
// no data, not a $0 batch), computes the mean per-plan cost across them,
// and returns a (low, high) range of mean ± 25%. batchCount is the number
// of archive entries that actually contributed to the mean.
//
// Returns (0, 0, 0) when no archive entries with TotalUSD > 0 exist or
// when the archive directory is missing. Callers should render
// "(no prior batches)" in that case.
func EstimatePerPlanUSD(root string, lookback int) (low, high float64, batchCount int) {
	if lookback <= 0 {
		lookback = 5
	}

	archiveDir := filepath.Join(root, ".springfield", "archive")
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, 0, 0
		}
		return 0, 0, 0
	}

	type sample struct {
		archivedAt time.Time
		perPlan    float64
	}
	var samples []sample

	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		path := filepath.Join(archiveDir, name)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var ae archiveEntry
		if jsonErr := json.Unmarshal(data, &ae); jsonErr != nil {
			continue
		}
		if ae.TotalUSD <= 0 || len(ae.Plans) == 0 {
			continue
		}
		// Prefer archived_at from the JSON entry (authoritative; survives
		// file copies/syncs) and fall back to file mod-time only when the
		// field is absent or zero (pre-PR or hand-edited archives).
		var t time.Time
		if ae.ArchivedAt != nil && !ae.ArchivedAt.IsZero() {
			t = *ae.ArchivedAt
		} else if info, statErr := ent.Info(); statErr == nil {
			t = info.ModTime()
		} else {
			continue
		}
		samples = append(samples, sample{
			archivedAt: t,
			perPlan:    ae.TotalUSD / float64(len(ae.Plans)),
		})
	}

	if len(samples) == 0 {
		return 0, 0, 0
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i].archivedAt.After(samples[j].archivedAt) })
	if len(samples) > lookback {
		samples = samples[:lookback]
	}

	var sum float64
	for _, s := range samples {
		sum += s.perPlan
	}
	mean := sum / float64(len(samples))
	return mean * 0.75, mean * 1.25, len(samples)
}
