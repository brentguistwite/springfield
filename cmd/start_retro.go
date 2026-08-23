package cmd

import (
	"fmt"
	"io"
	"path/filepath"

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
// Any error is surfaced as a grep-friendly "warning: retro:" line on w (matching
// the archive-warning style already used on this path) and then swallowed.
func emitBatchRetro(w io.Writer, root, batchID string) {
	batchDir := filepath.Join(batch.ArchiveDir(root), batchID)
	if _, err := retro.Persist(batchDir); err != nil {
		fmt.Fprintf(w, "warning: retro: batch %q: %v\n", batchID, err)
	}
}
