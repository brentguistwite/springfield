package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
	"springfield/internal/features/retro"
)

// retroRootGlobs are the machine project-root globs the retro aggregator scans
// for cross-project recurrence. They mirror the two Documents trees where
// Springfield projects live (work under flosports/, personal under personal/);
// each match's .springfield/archive is scanned for finished-batch retro.json.
var retroRootGlobs = []string{
	filepath.Join("Documents", "flosports", "*"),
	filepath.Join("Documents", "personal", "*"),
}

// runRetro is the completion-path retrospective step, gated by the [retro] block.
//
//   - enabled = false skips retro entirely: no retro.json is written and no vault
//     item is filed. This is the single gate the config's EnabledOrDefault feeds.
//   - enabled (the default) always persists the per-batch retro.json beside the
//     freshly archived batch (see emitBatchRetro).
//   - when items_dir is also configured (FilingEnabled), it then rolls the machine's
//     cross-project retro corpus up and files any above-threshold recurring pattern
//     as a vault ticket.
//
// The whole step is warning-only: nothing here alters the batch outcome or exit
// code (see emitBatchRetro / emitRecurringFiling).
func runRetro(w io.Writer, cfg config.RetroConfig, root, batchID string) {
	if !cfg.EnabledOrDefault() {
		return
	}
	emitBatchRetro(w, root, batchID)
	if cfg.FilingEnabled() {
		emitRecurringFiling(w, retroAggregateRoots(), cfg.ItemsDir)
	}
}

// retroAggregateRoots expands retroRootGlobs against the operator's home dir into
// the concrete project roots the aggregator scans. It always returns a non-nil
// slice (empty when no project dirs exist) so FileRecurring reads it as "nothing
// to scan" rather than the nil "nothing was asked" caller mistake.
func retroAggregateRoots() []string {
	roots := []string{}
	home, err := os.UserHomeDir()
	if err != nil {
		return roots
	}
	for _, glob := range retroRootGlobs {
		matches, err := filepath.Glob(filepath.Join(home, glob))
		if err != nil {
			continue // a malformed pattern our fixed joins never produce
		}
		for _, m := range matches {
			if info, err := os.Stat(m); err == nil && info.IsDir() {
				roots = append(roots, m)
			}
		}
	}
	return roots
}

// emitRecurringFiling files above-threshold recurring patterns across the given
// roots into itemsDir and surfaces the outcome on w. It is warning-only: a filing
// error is reported and swallowed, and a successful pass prints a grep-friendly
// count so the operator can see when a ticket was minted or refreshed.
func emitRecurringFiling(w io.Writer, roots []string, itemsDir string) {
	// The zero since scans all archived history; v1 applies no recency window
	// (the filer dedups, so refiling the same corpus is idempotent).
	results, err := retro.FileRecurring(roots, itemsDir, time.Time{})
	if err != nil {
		fmt.Fprintf(w, "warning: retro: filing recurring patterns: %v\n", err)
	}
	if len(results) == 0 {
		return
	}
	created := 0
	for _, r := range results {
		if r.Created {
			created++
		}
	}
	fmt.Fprintf(w, "retro: filed %d recurring pattern(s) (%d new) under %s\n",
		len(results), created, itemsDir)
}

// emitBatchRetro extracts, classifies, and persists the retrospective for a
// just-finalized batch as retro.json beside its archive entry under
// .springfield/archive/<batchID>/. It runs only on the completion path, after
// FinalizeBatch has durably archived the batch, and it is the sole writer of
// retro.json.
//
// Failure posture is warning-only: retro is a derived, best-effort artifact, so
// a failed extraction or write must never alter the batch outcome or exit code.
// It surfaces two grep-friendly "warning: retro:" lines on w (matching the
// archive-warning style already used on this path) and then swallows them:
//   - a hard [retro.Persist] error (an unwritable retro.json), and
//   - a soft extraction degradation — [retro.Extract] tolerates a missing/corrupt
//     archive.json or unlocatable evidence by recording each in Report.Degraded
//     rather than returning an error, so those extraction failures would
//     otherwise vanish silently. Surfacing the Degraded notes is what makes the
//     "warn on extraction failure" contract observable in production, where a
//     hard error effectively never fires.
func emitBatchRetro(w io.Writer, root, batchID string) {
	batchDir := filepath.Join(batch.ArchiveDir(root), batchID)
	rep, err := retro.Persist(batchDir)
	if err != nil {
		fmt.Fprintf(w, "warning: retro: batch %q: %v\n", batchID, err)
		return
	}
	if rep != nil && len(rep.Degraded) > 0 {
		fmt.Fprintf(w, "warning: retro: batch %q extracted with %d gap(s): %s\n",
			batchID, len(rep.Degraded), strings.Join(rep.Degraded, "; "))
	}
}
