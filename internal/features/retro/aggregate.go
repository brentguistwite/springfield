package retro

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Pattern is a cross-batch, cross-project rollup of one classifier pattern key.
//
// Where a [Finding] is a single classifier verdict over one batch's [Report],
// a Pattern folds every finding sharing a PatternKey — across many batches and
// many project roots — into one row: how often it fired, which batches and
// projects it touched, and when it was last seen. It is the corpus-level view a
// human scans to find the recurring failure modes worth fixing at the source.
type Pattern struct {
	// Key is the classifier's stable machine key (e.g. "iteration-cap"), the
	// same [Finding.PatternKey] value the rollup groups on.
	Key string `json:"key"`
	// Occurrences is the number of findings carrying Key across every in-window
	// report scanned — the raw frequency of the pattern. A batch that emits the
	// same key twice contributes two occurrences, so this can exceed the batch
	// count; a batch-level finding with no plans still counts as one.
	Occurrences int `json:"occurrences"`
	// Batches is the sorted set of distinct batch IDs whose report tripped Key.
	Batches []string `json:"batches"`
	// Projects is the sorted set of distinct project roots (the roots passed to
	// [Aggregate]) that contributed at least one occurrence of Key.
	Projects []string `json:"projects"`
	// LastSeen is the most recent report time (archived-at) among the findings
	// folded into this pattern — the freshness signal for the row.
	LastSeen time.Time `json:"last_seen"`
}

// Aggregate scans one or more project roots' archived retrospectives and rolls
// them up into per-pattern-key [Pattern] stats.
//
// For each root it globs <root>/.springfield/archive/*/retro.json — the durable
// output [WriteReport] leaves beside each finished batch's archive — and folds
// every [Finding] in every report whose archived-at time is strictly after
// since into the pattern keyed by its PatternKey: bumping the occurrence count,
// recording the batch ID and project root, and advancing last-seen.
//
// The scan is deliberately tolerant, because a cross-root corpus is assembled
// from repos that may not all exist or may carry a half-written file:
//   - a nonexistent root, or one with no .springfield/archive tree, contributes
//     nothing and is skipped silently (an empty glob, not an error);
//   - a single corrupt or unreadable retro.json is skipped on its own, so one
//     bad file never sinks the batch beside it or the roots beside that.
//
// The only error is an unusable input — a nil roots slice, the caller mistake
// that means "nothing was asked" rather than "nothing was found". An empty
// (non-nil) roots slice, or roots that yield no reports, returns an empty
// result and no error.
//
// Aggregate is a pure reader: it opens files read-only and never writes,
// creates, or renames anything on disk.
//
// Results are sorted by Occurrences descending, ties broken by Key ascending,
// so the same corpus always aggregates to the same ordered slice.
func Aggregate(roots []string, since time.Time) ([]Pattern, error) {
	if roots == nil {
		return nil, fmt.Errorf("retro: roots must not be nil")
	}

	// Keyed accumulators, resolved into sorted Pattern rows at the end.
	type acc struct {
		occurrences int
		batches     map[string]struct{}
		projects    map[string]struct{}
		lastSeen    time.Time
	}
	byKey := map[string]*acc{}

	for _, root := range roots {
		root = filepath.Clean(root)
		matches, err := filepath.Glob(filepath.Join(root, ".springfield", "archive", "*", retroFileName))
		if err != nil {
			// Glob only errors on a malformed pattern, which our fixed join never
			// produces; treat a nonexistent root as an empty corpus regardless.
			continue
		}
		for _, path := range matches {
			data, err := os.ReadFile(path)
			if err != nil {
				continue // vanished or unreadable between glob and read; skip this file only
			}
			var r Report
			if err := json.Unmarshal(data, &r); err != nil {
				continue // one corrupt retro.json never sinks the corpus
			}
			// The report time is the batch's archived-at; only reports strictly
			// inside the since window contribute.
			if !r.ArchivedAt.After(since) {
				continue
			}
			for _, f := range r.Findings {
				a := byKey[f.PatternKey]
				if a == nil {
					a = &acc{batches: map[string]struct{}{}, projects: map[string]struct{}{}}
					byKey[f.PatternKey] = a
				}
				a.occurrences++
				if r.BatchID != "" {
					a.batches[r.BatchID] = struct{}{}
				}
				a.projects[root] = struct{}{}
				if r.ArchivedAt.After(a.lastSeen) {
					a.lastSeen = r.ArchivedAt
				}
			}
		}
	}

	patterns := make([]Pattern, 0, len(byKey))
	for key, a := range byKey {
		patterns = append(patterns, Pattern{
			Key:         key,
			Occurrences: a.occurrences,
			Batches:     sortedKeys(a.batches),
			Projects:    sortedKeys(a.projects),
			LastSeen:    a.lastSeen,
		})
	}
	sort.Slice(patterns, func(i, j int) bool {
		if patterns[i].Occurrences != patterns[j].Occurrences {
			return patterns[i].Occurrences > patterns[j].Occurrences
		}
		return patterns[i].Key < patterns[j].Key
	})
	return patterns, nil
}

// sortedKeys returns a set's members as a sorted slice, giving Pattern.Batches
// and Pattern.Projects a deterministic order independent of map iteration.
func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
