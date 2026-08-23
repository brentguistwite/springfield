package retro

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// FileRecurring scans the archived retrospectives under the given project roots,
// rolls their findings up per pattern key, and files a convention-compliant vault
// ticket for every key that clears the recurrence threshold ([AboveThreshold]:
// >= [MinOccurrences] findings across >= [MinBatches] distinct batches). It is the
// actuation half of the retro loop wired end to end — the cross-root read of
// [Aggregate] joined to the filing write of [Config.File] — so the caller (the
// completion path) hands it explicit roots and an items dir and stays thin.
//
// roots are the project roots to scan (each <root>/.springfield/archive/*/retro.json);
// itemsDir is the vault items directory to file into; since narrows the corpus to
// reports archived strictly after it (pass the zero time to scan all history).
//
// Tolerance mirrors [Aggregate]: a nonexistent root, an absent archive tree, or a
// single corrupt/unreadable retro.json contributes nothing and never sinks the
// scan. The only hard errors are unusable inputs — nil roots, or an empty itemsDir
// (the filer is disabled) — surfaced before any file is touched. A per-item filing
// error does not abort the rest: every qualifying item is attempted and the errors
// are joined into the returned error alongside the [FileResult]s that did land.
//
// Results are returned in ascending pattern-key order so the same corpus always
// files in the same, diffable order.
func FileRecurring(roots []string, itemsDir string, since time.Time) ([]FileResult, error) {
	if roots == nil {
		return nil, fmt.Errorf("retro: roots must not be nil")
	}
	filer := Config{ItemsDir: itemsDir}
	if !filer.Enabled() {
		return nil, errors.New("retro: filer disabled (empty ItemsDir)")
	}

	// key -> batchID -> the single occurrence receipt for that batch. A batch's
	// repeated findings for the same key bump that receipt's Count rather than
	// mint a second receipt, matching the per-batch shape [Config.File] logs.
	byKey := map[string]map[string]*Occurrence{}
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
			// A receipt is keyed by batch id, so a report missing one cannot be
			// filed; the since window is a strict lower bound (see Aggregate).
			if r.BatchID == "" || !r.ArchivedAt.After(since) {
				continue
			}
			for _, f := range r.Findings {
				m := byKey[f.PatternKey]
				if m == nil {
					m = map[string]*Occurrence{}
					byKey[f.PatternKey] = m
				}
				occ := m[r.BatchID]
				if occ == nil {
					occ = &Occurrence{BatchID: r.BatchID, Date: r.ArchivedAt, Project: root}
					m[r.BatchID] = occ
				}
				occ.Count++
			}
		}
	}

	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var results []FileResult
	var errs []error
	for _, key := range keys {
		occByBatch := byKey[key]

		total := 0
		batches := make([]string, 0, len(occByBatch))
		occurrences := make([]Occurrence, 0, len(occByBatch))
		for batchID, o := range occByBatch {
			total += o.Count
			batches = append(batches, batchID)
			occurrences = append(occurrences, *o)
		}
		// Gate on the same named boundary Aggregate's rows are judged by, so the
		// filing bar and the corpus view can never drift apart.
		if !AboveThreshold(Pattern{Occurrences: total, Batches: batches}) {
			continue
		}

		res, err := filer.File(Item{Key: key, Occurrences: occurrences})
		if err != nil {
			errs = append(errs, fmt.Errorf("retro: file %q: %w", key, err))
			continue
		}
		results = append(results, res)
	}
	return results, errors.Join(errs...)
}
