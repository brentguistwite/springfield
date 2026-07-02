package statusview

import (
	"fmt"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
)

// Archived projects a completed-and-archived batch's ArchiveEntry into the same
// View envelope used for the active batch, so a status consumer sees one stable
// per-ticket list across the lifecycle: live PlanState while running, the
// archive entry once the run cursor is cleared. State is "archived".
func Archived(entry batch.ArchiveEntry) View {
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
	}
	if entry.TotalUSD > 0 || len(entry.CostBreakdown) > 0 {
		v.Spend = &SpendView{TotalUSD: entry.TotalUSD, PerAdapter: entry.CostBreakdown}
	}
	return v
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
