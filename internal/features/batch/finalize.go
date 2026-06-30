package batch

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"springfield/internal/features/conductor"
	"springfield/internal/features/cost"
)

// FinalizeBatch is the completion-path teardown for a batch that finished
// successfully. Unlike [ArchiveBatchNormalized] (still used for orphan,
// --replace, and tamper paths), it PRESERVES the per-ticket trail instead of
// reaping it:
//
//  1. snapshot a per-plan record (branch/base/status/evidence) from PlanState —
//     MUST precede deregister, which clears PlanState;
//  2. relocate .springfield/execution/plans/<key> →
//     .springfield/archive/<batchID>/plans/<key> (os.Rename, copy+remove on a
//     cross-device EXDEV) so evidence survives teardown;
//  3. write the enriched ArchiveEntry (rollup + per-plan records);
//  4. deregister the batch's OWN plan units (Fix 2) + SaveConfig;
//  5. ClearRun.
//
// Archive durability is BEST-EFFORT, matching the prior completion contract:
// relocate/archive/deregister failures are warned to warnW (which carries a
// "warning: archive" prefix the operator can grep) and the on-disk forensics
// (plan dir, units) are preserved rather than deleted. Only ClearRun is fatal —
// a returned non-nil error means the run cursor could not be cleared.
//
// rollup MUST be computed by the caller BEFORE this call — relocating evidence
// first would make cost.ComputeRollup (which walks execution/plans/<key>) read
// $0. project is the live project whose state runBatch saved; b.PlanIDs scopes
// every batch-owned mutation so standalone `plans add` units are left alone.
func FinalizeBatch(rootDir string, b Batch, project *conductor.Project, rollup *cost.Rollup, warnW io.Writer) error {
	if project == nil || project.Config == nil || project.State == nil {
		return fmt.Errorf("finalize batch %s: project is not loaded", b.ID)
	}
	warn := func(format string, args ...any) {
		if warnW != nil {
			fmt.Fprintf(warnW, format, args...)
		}
	}

	// (1) Snapshot per-plan records BEFORE any teardown clears PlanState.
	titleByID := make(map[string]string, len(project.Config.PlanUnits))
	for _, u := range project.Config.PlanUnits {
		titleByID[u.ID] = u.Title
	}
	records := make([]ArchivePlan, 0, len(b.PlanIDs))
	for _, planID := range b.PlanIDs {
		rec := ArchivePlan{ID: planID, Title: titleByID[planID]}
		if st := project.State.Plans[planID]; st != nil {
			rec.Status = string(st.Status)
			rec.Branch = st.Branch
			rec.BaseRef = st.BaseRef
		}
		records = append(records, rec)
	}

	// (2) Relocate evidence into the durable archive namespace per plan
	// (best-effort: a failure leaves the evidence in place, EvidencePath empty).
	for i := range records {
		durable, moved, err := relocatePlanEvidence(rootDir, b.ID, records[i].ID)
		if err != nil {
			warn("warning: archive: relocate evidence for plan %s: %v\n", records[i].ID, err)
			continue
		}
		if moved {
			records[i].EvidencePath = durable
		}
	}

	// (3) Write the enriched archive entry (best-effort, idempotent).
	archiveOK := true
	if err := writeEnrichedArchive(rootDir, b, rollup, records); err != nil {
		warn("warning: archive completed batch %q: %v\n", b.ID, err)
		archiveOK = false
	}

	// Only after a durable archive do we perform the destructive teardown:
	// remove the compiled batch plan dir and deregister the batch's own units.
	// On archive failure these are preserved so nothing is lost without a
	// record (mirrors the prior ArchiveBatchNormalized best-effort contract).
	if archiveOK {
		if paths, err := NewPaths(rootDir, b.ID); err == nil {
			if rmErr := os.RemoveAll(paths.PlanDir()); rmErr != nil {
				warn("warning: archive: remove batch plan dir: %v\n", rmErr)
			}
		}
		// (4) Deregister the batch's own units (Fix 2). Scoped to b.PlanIDs so
		// standalone `plans add` units survive. RemovePlanUnit also clears
		// State.Plans — safe now that records are snapshotted. A unit already
		// absent (e.g. a re-run after a prior partial finalize) is tolerated.
		for _, planID := range b.PlanIDs {
			_ = project.RemovePlanUnit(planID)
		}
		if err := project.SaveConfig(); err != nil {
			warn("warning: archive: deregister batch plan units: %v\n", err)
		}
	}

	// (5) Clear the run cursor — the only fatal step.
	return ClearRun(rootDir)
}

// writeEnrichedArchive writes the per-batch archive entry (enriched with the
// per-plan records) via the same single-writer/O_EXCL path as
// ArchiveBatchNormalized. Returns an error when the archive dir or entry file
// cannot be written; the caller treats that as a best-effort warning.
func writeEnrichedArchive(rootDir string, b Batch, rollup *cost.Rollup, records []ArchivePlan) error {
	if err := os.MkdirAll(ArchiveDir(rootDir), 0o755); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}
	archivePath := StableArchivePath(rootDir, b.ID)
	entry := newArchiveEntry(b, "completed", rollup, time.Now().UTC())
	entry.Plans = records
	existed, err := writeJSONExclusive(archivePath, entry)
	if err != nil {
		return err
	}
	if existed {
		return maybeWriteArchiveSibling(archivePath, entry)
	}
	return nil
}

// newArchiveEntry builds the common ArchiveEntry shell (id/title/time/reason +
// rollup cost fields). Callers add per-plan records as needed. Shared by
// FinalizeBatch and ArchiveBatchNormalized so the cost-copy logic lives once.
func newArchiveEntry(b Batch, reason string, rollup *cost.Rollup, now time.Time) ArchiveEntry {
	entry := ArchiveEntry{
		BatchID:    b.ID,
		Title:      b.Title,
		ArchivedAt: now,
		Reason:     reason,
	}
	if rollup != nil {
		entry.TotalUSD = rollup.TotalUSD
		if len(rollup.PerAdapter) > 0 {
			entry.CostBreakdown = make(map[string]float64, len(rollup.PerAdapter))
			for k, v := range rollup.PerAdapter {
				entry.CostBreakdown[k] = v
			}
		}
	}
	return entry
}

// relocatePlanEvidence moves a plan's execution evidence from
// .springfield/execution/plans/<key> to .springfield/archive/<batchID>/plans/<key>
// and returns the durable project-relative destination. Returns moved=false
// when the plan produced no evidence. os.Rename is tried first; a cross-device
// (EXDEV) error falls back to a recursive copy-then-remove.
func relocatePlanEvidence(rootDir, batchID, planID string) (durableRel string, moved bool, err error) {
	planKey := SanitizeID(planID)
	if planKey == "" {
		return "", false, nil
	}
	src := filepath.Join(rootDir, springfieldDir, "execution", "plans", planKey)
	if _, statErr := os.Stat(src); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, statErr
	}

	dstRel := filepath.Join(springfieldDir, "archive", batchID, "plans", planKey)
	dst := filepath.Join(rootDir, dstRel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", false, err
	}
	// Clear any stale destination from a prior partial finalize so Rename
	// (which fails onto a non-empty dir) and the copy fallback are clean.
	_ = os.RemoveAll(dst)

	if renErr := os.Rename(src, dst); renErr != nil {
		if !errors.Is(renErr, syscall.EXDEV) {
			return "", false, renErr
		}
		// Cross-device move: copy the tree then remove the source.
		if cpErr := copyTree(src, dst); cpErr != nil {
			return "", false, cpErr
		}
		if rmErr := os.RemoveAll(src); rmErr != nil {
			return "", false, rmErr
		}
	}
	return dstRel, true, nil
}

// copyTree recursively copies the directory tree at src into dst, preserving
// file modes. Used only on the EXDEV (cross-filesystem) rename fallback.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			mode := fs.FileMode(0o755)
			if info, infoErr := d.Info(); infoErr == nil {
				mode = info.Mode().Perm()
			}
			return os.MkdirAll(target, mode)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	info, statErr := in.Stat()
	mode := fs.FileMode(0o644)
	if statErr == nil {
		mode = info.Mode().Perm()
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
