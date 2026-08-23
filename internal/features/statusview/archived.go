package statusview

import (
	"fmt"
	"path/filepath"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/retro"
)

// Archived projects a completed-and-archived batch's ArchiveEntry into the same
// View envelope used for the active batch, so a status consumer sees one stable
// per-ticket list across the lifecycle: live PlanState while running, the
// archive entry once the run cursor is cleared. State is "archived".
//
// root is the project root, used to locate the batch's sibling retro.json under
// .springfield/archive/<batch-id>/ and fold its digest into the view. It is
// read-only and tolerant: an absent or corrupt retro.json (or an empty root)
// leaves Retro null — the archived projection never fails on a bad retro file.
func Archived(entry batch.ArchiveEntry, root string) View {
	plans := make([]PlanView, 0, len(entry.Plans))
	for _, ap := range entry.Plans {
		plans = append(plans, PlanFromArchive(ap, entry.BatchMode))
	}
	v := View{
		SchemaVersion: SchemaVersion,
		State:         "archived",
		Summary:       fmt.Sprintf("Batch %s archived (%d plans)", entry.BatchID, len(entry.Plans)),
		Batch:         &BatchView{ID: entry.BatchID, Title: entry.Title},
		Plans:         plans,
		Retro:         retroView(root, entry.BatchID),
	}
	if entry.TotalUSD > 0 || len(entry.CostBreakdown) > 0 {
		v.Spend = &SpendView{TotalUSD: entry.TotalUSD, PerAdapter: entry.CostBreakdown}
	}
	return v
}

// retroView loads the archived batch's retro.json digest, degrading to nil on any
// problem (empty root, absent/corrupt file, or a report with no findings). The
// summarization logic lives in the retro package; this only maps its result into
// the view-model, so the two surfaces share one definition of "the top pattern".
func retroView(root, batchID string) *RetroView {
	if root == "" {
		return nil
	}
	rep, err := retro.Load(filepath.Join(batch.ArchiveDir(root), batchID))
	if err != nil || rep == nil {
		return nil
	}
	s := retro.Summarize(rep)
	if s.TotalFindings == 0 {
		return nil
	}
	return &RetroView{Findings: s.TotalFindings, TopPattern: s.TopPatternKey, TopCount: s.TopCount}
}

// PlanFromArchive projects one archived per-plan record into the same PlanView
// shape buildPlan emits for live plans. The compact archive record carries no
// merge/cleanup ledger, so Merge/Review default to empty and Integration to
// "clean"; Status maps the recorded raw plan status to the board enum, using
// the batch's branch mode to distinguish a merged plan from a retained one.
func PlanFromArchive(ap batch.ArchivePlan, mode string) PlanView {
	title := ap.Title
	if title == "" {
		title = ap.ID
	}
	return PlanView{
		ID:           ap.ID,
		Title:        title,
		Status:       statusFromArchiveStatus(ap.Status, mode),
		Branch:       ap.Branch,
		BaseBranch:   ap.BaseRef,
		EvidencePath: ap.EvidencePath,
		Review:       ReviewView{},
		Merge:        MergeView{},
		Integration:  IntegrationView{State: "clean"},
	}
}

// statusFromArchiveStatus maps a stored raw plan-status string + batch mode to
// the public board enum. FinalizeBatch only archives a fully-completed batch
// (every plan integrated), so a completed record is terminal-integrated: it
// projects to StatusRetained in per-plan mode (the branch is standing, awaiting
// a PR — NOT merged into a base) and StatusMerged otherwise. An empty/legacy
// mode falls back to StatusMerged, preserving the prior projection.
func statusFromArchiveStatus(raw, mode string) string {
	switch conductor.PlanStatus(raw) {
	case conductor.StatusCompleted:
		if mode == string(config.BranchModePerPlan) {
			return StatusRetained
		}
		return StatusMerged
	case conductor.StatusFailed:
		return StatusFailed
	case conductor.StatusNeedsHuman:
		return StatusNeedsHuman
	case conductor.StatusRunning:
		return StatusRunning
	default:
		return StatusPending
	}
}
