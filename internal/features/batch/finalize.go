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
//  2. write the enriched ArchiveEntry (rollup + per-plan records) FIRST, before
//     any destructive teardown, so an entry-write failure leaves evidence in
//     place rather than stranded under archive/ with no entry;
//  3. relocate .springfield/execution/plans/<key> →
//     .springfield/archive/<batchID>/plans/<key> (os.Rename, copy+remove on a
//     cross-device EXDEV) so evidence survives teardown;
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
func FinalizeBatch(rootDir string, b Batch, project *conductor.Project, rollup *cost.Rollup, mode string, warnW io.Writer) error {
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
		// EvidencePath is the durable archive location the evidence lives at
		// AFTER relocation. It is computed here (without moving anything) so the
		// record can be written before the relocate runs — see step 2. The path
		// is recorded only when evidence actually exists (live, or already moved
		// by a prior partial finalize), so a plan with no evidence stays empty.
		if rel, ok := planEvidenceLocation(rootDir, b.ID, planID); ok {
			rec.EvidencePath = rel
		}
		records = append(records, rec)
	}

	// (2) Write the enriched archive entry FIRST — before any destructive
	// relocate/teardown. Writing the durable record before moving evidence is
	// what makes an entry-write failure safe: the evidence is left untouched in
	// execution/plans/<key> (preserved in place, matching the prior
	// ArchiveBatchNormalized best-effort contract) rather than stranded under
	// archive/ with no entry pointing at it.
	archiveOK := true
	if err := writeEnrichedArchive(rootDir, b, rollup, mode, records); err != nil {
		warn("warning: archive completed batch %q: %v\n", b.ID, err)
		archiveOK = false
	}

	// Only after a durable archive do we perform the destructive teardown.
	if archiveOK {
		// (3) Relocate evidence into the durable archive namespace. Idempotent
		// (no-op when already moved or absent) and best-effort — a failure
		// leaves the evidence discoverable in execution/plans/<key>.
		for _, planID := range b.PlanIDs {
			if err := relocatePlanEvidence(rootDir, b.ID, planID); err != nil {
				warn("warning: archive: relocate evidence for plan %s: %v\n", planID, err)
			}
		}
		// Remove the compiled batch plan dir (prd.json/context.md/batch.json).
		if paths, err := NewPaths(rootDir, b.ID); err == nil {
			if rmErr := os.RemoveAll(paths.PlanDir()); rmErr != nil {
				warn("warning: archive: remove batch plan dir: %v\n", rmErr)
			}
		}
		// (4) Deregister the batch's own units (Fix 2). Scoped to b.PlanIDs so
		// standalone `plans add` units survive. RemovePlanUnit also clears
		// State.Plans in memory — safe now that records are snapshotted. A unit
		// already absent (e.g. a re-run after a prior partial finalize) is
		// tolerated.
		for _, planID := range b.PlanIDs {
			_ = project.RemovePlanUnit(planID)
		}
		if err := project.SaveConfig(); err != nil {
			warn("warning: archive: deregister batch plan units: %v\n", err)
		}
		// SaveConfig persists only config.json; the State.Plans deletions above
		// live in a separate state.json and are lost unless flushed. Without
		// this, completed plan entries survive on disk and a later batch that
		// reuses a plan ID reads stale non-pending state (anyPlanStarted → forces
		// consolidate, drops --per-plan-branches). Best-effort, like SaveConfig.
		if err := project.SaveState(); err != nil {
			warn("warning: archive: persist cleared plan state: %v\n", err)
		}
	}

	// (5) Clear the run cursor — the only fatal step.
	return ClearRun(rootDir)
}

// writeEnrichedArchive writes the per-batch archive entry (enriched with the
// per-plan records) via the same single-writer/O_EXCL path as
// ArchiveBatchNormalized. Returns an error when the archive dir or entry file
// cannot be written; the caller treats that as a best-effort warning.
func writeEnrichedArchive(rootDir string, b Batch, rollup *cost.Rollup, mode string, records []ArchivePlan) error {
	if err := os.MkdirAll(ArchiveDir(rootDir), 0o755); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}
	archivePath := StableArchivePath(rootDir, b.ID)
	entry := newArchiveEntry(b, "completed", rollup, time.Now().UTC())
	entry.BatchMode = mode
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

// planEvidenceLocation returns the durable project-relative archive path for a
// plan's evidence and whether any evidence exists — at the live execution
// location OR already relocated under the archive namespace (a prior partial
// finalize). It performs NO move, so the path can be recorded in the archive
// entry before relocation runs. Empty/false when the plan produced no evidence.
func planEvidenceLocation(rootDir, batchID, planID string) (rel string, exists bool) {
	planKey := SanitizeID(planID)
	if planKey == "" {
		return "", false
	}
	rel = filepath.Join(springfieldDir, "archive", batchID, "plans", planKey)
	src := filepath.Join(rootDir, springfieldDir, "execution", "plans", planKey)
	dst := filepath.Join(rootDir, rel)
	if pathExists(src) || pathExists(dst) {
		return rel, true
	}
	return "", false
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// relocatePlanEvidence moves a plan's execution evidence from
// .springfield/execution/plans/<key> to .springfield/archive/<batchID>/plans/<key>.
// Idempotent: a no-op when the source is already gone (relocated by a prior
// call) or never existed, so a crash mid-finalize re-converges cleanly on the
// next start. os.Rename is tried first; a cross-device (EXDEV) error falls back
// to a recursive copy-then-remove.
func relocatePlanEvidence(rootDir, batchID, planID string) error {
	planKey := SanitizeID(planID)
	if planKey == "" {
		return nil
	}
	src := filepath.Join(rootDir, springfieldDir, "execution", "plans", planKey)
	if !pathExists(src) {
		return nil // already relocated or no evidence — nothing to do
	}
	dst := filepath.Join(rootDir, springfieldDir, "archive", batchID, "plans", planKey)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// src is the authoritative copy when both exist (a prior partial move):
	// clear any stale/partial destination before re-moving.
	_ = os.RemoveAll(dst)

	if renErr := os.Rename(src, dst); renErr != nil {
		if !errors.Is(renErr, syscall.EXDEV) {
			return renErr
		}
		// Cross-device move: copy the tree (fsync'd per file) then remove src.
		if cpErr := copyTree(src, dst); cpErr != nil {
			return cpErr
		}
		return os.RemoveAll(src)
	}
	return nil
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
		// A symlink must be recreated as a symlink: WalkDir does not follow it
		// (d.IsDir() is false even for a symlink-to-dir), so copyFile would
		// os.Open the target — failing with "is a directory" for a dir link, or
		// silently materializing a regular file for a file link. Preserve it.
		if d.Type()&fs.ModeSymlink != 0 {
			linkTarget, linkErr := os.Readlink(path)
			if linkErr != nil {
				return linkErr
			}
			return os.Symlink(linkTarget, target)
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
	// fsync before the caller removes the source: on the EXDEV path
	// os.RemoveAll(src) deletes the only other copy, so an unsynced dst could
	// be truncated/empty after a power loss between write and flush.
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
