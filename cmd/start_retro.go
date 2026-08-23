package cmd

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"springfield/internal/features/batch"
	"springfield/internal/features/retro"
)

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
