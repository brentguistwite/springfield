package statusview

import (
	"strconv"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planmerge"
	"springfield/internal/features/cost"
)

// Idle is the view when no batch is active (the plan-registry case).
func Idle() View {
	return View{
		SchemaVersion: SchemaVersion,
		State:         "idle",
		Summary:       "No active batch.",
	}
}

// Orphan is the view when run.json references a batch whose batch.json is
// missing. Spend is intentionally absent (the evidence path is unresolvable).
func Orphan(run batch.Run) View {
	v := View{
		SchemaVersion: SchemaVersion,
		State:         "orphan",
		Summary:       "Batch " + run.ActiveBatchID + " orphaned — batch.json missing. Run \"springfield recover\".",
		Batch:         &BatchView{ID: run.ActiveBatchID},
		Flags:         buildFlags(run),
	}
	return v
}

func buildFlags(run batch.Run) *FlagsView {
	f := &FlagsView{CostCapped: run.CostCapped}
	if run.FatalError != "" {
		fe := run.FatalError
		f.FatalError = &fe
	}
	return f
}

// buildActiveFlags builds FlagsView for an active batch. fatalError is the
// already-suppressed value (empty when the text path would suppress it);
// costCapped comes directly from run.CostCapped; lastRetry mirrors the
// text path's "Recent retries:" block (active batches only, not orphan).
func buildActiveFlags(fatalError string, costCapped bool, lastRetry []string) *FlagsView {
	f := &FlagsView{CostCapped: costCapped}
	if fatalError != "" {
		fe := fatalError
		f.FatalError = &fe
	}
	if len(lastRetry) > 0 {
		f.LastRetry = lastRetry
	}
	return f
}

// ComposeStatus is the single canonical projection of a plan's lifecycle into
// the public board-status enum — THE source of truth for "what state is this
// plan in", consumed by both the JSON view-model and the text status renderer
// so the two surfaces cannot disagree. completed is split into done/merged via
// IsIntegrated(); a started-but-non-terminal plan (running/interrupted) is
// running when a live springfield process owns the control-plane lock and
// stalled otherwise (the owning process died without recording a terminal
// result — needs resume or abandon, distinct from needs-human's semantic review
// stop). Any unrecognized internal state surfaces as needs-human rather than
// masquerading as good.
func ComposeStatus(ps *conductor.PlanState, live bool) string {
	if ps == nil {
		return StatusPending
	}
	switch ps.Status {
	case conductor.StatusPending:
		return StatusPending
	case conductor.StatusRunning, conductor.StatusInterrupted:
		if live {
			return StatusRunning
		}
		return StatusStalled
	case conductor.StatusFailed:
		return StatusFailed
	case conductor.StatusNeedsHuman:
		return StatusNeedsHuman
	case conductor.StatusCompleted:
		if ps.IsIntegrated() {
			// A plan retained in per-plan mode is integrated (Retain records a
			// terminal MergeSucceeded + Cleanup) but its branch was kept, NOT
			// merged into a base. Distinguish it so the live/text surfaces match
			// the archived projection — otherwise a controller polling an active
			// per-plan batch sees "merged" for a standing branch.
			if ps.Merge != nil && ps.Merge.Mode == planmerge.ModeStandalone {
				return StatusRetained
			}
			return StatusMerged
		}
		return StatusDone
	default:
		return StatusNeedsHuman
	}
}

// ParallelInFlight is THE definition of the "parallel" signal, consumed by
// both the JSON view-model (progress.parallel_in_flight) and the text
// renderer's Current-line label — so the two surfaces cannot disagree about
// parallelism any more than they can disagree about per-plan status. It is
// true when the current phase runs plans concurrently AND 2+ of them are in
// the running state per ComposeStatus.
//
// It keys off ComposeStatus, NOT batch.ClassifyPlan: the two differ on
// StatusInterrupted-while-live (ComposeStatus: running; ClassifyPlan: pending)
// and on StatusRunning-while-dead (ComposeStatus: stalled; ClassifyPlan:
// in-flight). Sourcing the signal from ComposeStatus keeps it consistent with
// the per-plan statuses both surfaces render — a phase is never reported
// "parallel" while the plans it refers to read "stalled". The current phase is
// the first not fully integrated, reusing batch.ComputeProgress so phase
// detection stays single-source too.
func ParallelInFlight(b batch.Batch, state *conductor.State, live bool) bool {
	idx := batch.ComputeProgress(b, state).CurrentPhaseIdx
	if idx < 0 || idx >= len(b.Phases) {
		return false
	}
	ph := b.Phases[idx]
	if ph.Mode != batch.PhaseParallel {
		return false
	}
	running := 0
	for _, id := range ph.Plans {
		var ps *conductor.PlanState
		if state != nil {
			ps = state.Plans[id]
		}
		if ComposeStatus(ps, live) == StatusRunning {
			running++
		}
	}
	return running >= 2
}

// deriveIntegration rolls post-merge disposition into one trustworthy signal.
// A plan whose merge succeeded but is NOT integrated (cleanup or source-sync
// failed) is "needs_attention" — the case merge.status:"succeeded" alone would
// mask. Everything else (not yet merged, merge pending/refused/failed, or
// cleanly integrated) is "clean": those are either benign or already flagged by
// status/merge.
func deriveIntegration(ps *conductor.PlanState) IntegrationView {
	if ps == nil || ps.Merge == nil {
		return IntegrationView{State: "clean"}
	}
	if ps.Merge.Status == conductor.MergeSucceeded && !ps.IsIntegrated() {
		// Reason mirrors IsIntegrated's not-integrated causes, in its order, so
		// the consumer's remediation matches the actual fault. cleanup-unrecorded
		// (Cleanup==nil, the ledger was never persisted) is distinct from
		// cleanup-failed (cleanup ran and failed): the former is an audit-trail
		// gap to verify, the latter a failure to investigate. Mislabeling the
		// first as the second sends the consumer chasing a cleanup error that
		// never happened.
		reason := "cleanup-failed"
		switch {
		case ps.Merge.SourceSyncStatus == "failed":
			reason = "source-sync-failed"
		case ps.Cleanup == nil:
			reason = "cleanup-unrecorded"
		}
		return IntegrationView{State: "needs_attention", Reason: &reason}
	}
	return IntegrationView{State: "clean"}
}

const reviewHaltExitReason = "review-needs-human"

// verifyHaltExitReason is the ExitReason the runner records when a plan halts at
// the verify gate (mirrors the "verify-needs-human" tag set in planrun.runner).
const verifyHaltExitReason = "verify-needs-human"

func buildPlan(id, title string, ps *conductor.PlanState, live bool) PlanView {
	if title == "" {
		title = id
	}
	pv := PlanView{
		ID:          id,
		Title:       title,
		Status:      ComposeStatus(ps, live),
		Verify:      deriveVerify(ps),
		Review:      deriveReview(ps),
		Merge:       deriveMerge(ps),
		Integration: deriveIntegration(ps),
	}
	if ps != nil {
		pv.Branch = ps.Branch
		pv.BaseBranch = ps.BaseRef
		pv.BaseHead = ps.BaseHead
		pv.Attempt = ps.Attempts
		pv.EvidencePath = ps.EvidencePath
		if ps.Error != "" {
			e := ps.Error
			pv.LastError = &e
		}
	}
	return pv
}

// deriveReview surfaces only the terminal halt (per-iteration pass/revise are
// not persisted). verdict/reason come straight from PlanState — no evidence
// parsing.
func deriveReview(ps *conductor.PlanState) ReviewView {
	if ps == nil || ps.ExitReason != reviewHaltExitReason {
		return ReviewView{}
	}
	verdict := "halt"
	rv := ReviewView{Verdict: &verdict}
	if ps.Error != "" {
		reason := ps.Error
		rv.Reason = &reason
	}
	return rv
}

// deriveVerify mirrors deriveReview for the objective verify gate: it surfaces
// the terminal halt only. Like review-errored, verify-errored is an infra
// failure that surfaces via status "failed" + last_error, not a halt verdict,
// so only verify-needs-human sets a verdict here.
func deriveVerify(ps *conductor.PlanState) VerifyView {
	if ps == nil || ps.ExitReason != verifyHaltExitReason {
		return VerifyView{}
	}
	verdict := "halt"
	vv := VerifyView{Verdict: &verdict}
	if ps.Error != "" {
		reason := ps.Error
		vv.Reason = &reason
	}
	return vv
}

func deriveMerge(ps *conductor.PlanState) MergeView {
	if ps == nil || ps.Merge == nil {
		return MergeView{}
	}
	status := string(ps.Merge.Status)
	mv := MergeView{Status: &status}
	if ps.Merge.Reason != "" {
		reason := ps.Merge.Reason
		mv.Reason = &reason
	}
	if ps.Merge.Error != "" {
		errMsg := ps.Merge.Error
		mv.Error = &errMsg
	}
	return mv
}

// ActiveInput carries the already-loaded sources for the active batch. The
// caller (cmd/status.go) performs all IO; Active is a pure projection.
//
// FatalError is the effective fatal error for the active batch — the caller
// (cmd/status.go) must apply the same suppression logic as the text path
// (batchHasFailedPlan) before setting this field. An empty string means
// no fatal_error should appear in the JSON output.
type ActiveInput struct {
	Batch      batch.Batch
	Run        batch.Run
	State      *conductor.State
	Units      []conductor.PlanUnit
	Rollup     cost.Rollup
	HasRollup  bool
	FatalError string
	// Live is true when a live springfield process owns the control-plane lock
	// (lock.Inspect confirmed a holder). The caller (cmd/status.go) probes this
	// read-only. It gates running vs stalled: a started-but-non-terminal plan is
	// running only when a live process exists, otherwise stalled.
	Live bool
}

// Active builds the view-model for a live batch. Plan order follows
// Batch.PlanIDs (the canonical order). Spend is omitted when no rollup.
func Active(in ActiveInput) View {
	titles := make(map[string]string, len(in.Units))
	for _, u := range in.Units {
		titles[u.ID] = u.Title
	}

	plans := make([]PlanView, 0, len(in.Batch.PlanIDs))
	for _, id := range in.Batch.PlanIDs {
		var ps *conductor.PlanState
		if in.State != nil {
			ps = in.State.Plans[id]
		}
		plans = append(plans, buildPlan(id, titles[id], ps, in.Live))
	}

	v := View{
		SchemaVersion: SchemaVersion,
		State:         "active",
		Batch:         &BatchView{ID: in.Batch.ID, Title: in.Batch.Title},
		Flags:         buildActiveFlags(in.FatalError, in.Run.CostCapped, in.Run.LastRetry),
		Plans:         plans,
	}
	// Mirror the text path: suppress progress and spend when state is unavailable.
	// ComputeProgress with nil state produces a misleading all-pending result.
	if in.State != nil {
		prog := batch.ComputeProgress(in.Batch, in.State)
		v.Summary = activeSummary(in.Batch, prog)
		v.Progress = &ProgressView{
			Completed:        prog.DonePlans,
			Total:            prog.TotalPlans,
			PhaseIndex:       prog.CurrentPhaseIdx,
			PhaseTotal:       prog.TotalPhases,
			AllDone:          prog.AllDone,
			ParallelInFlight: ParallelInFlight(in.Batch, in.State, in.Live),
		}
		if in.HasRollup {
			v.Spend = &SpendView{
				TotalUSD:     in.Rollup.TotalUSD,
				PerAdapter:   pricedAdapters(in.Rollup.PerAdapter),
				Iterations:   in.Rollup.Iterations,
				UnpricedRuns: in.Rollup.UnpricedRuns,
				SkippedFiles: in.Rollup.SkippedFiles,
			}
		}
	} else {
		v.Summary = "Batch " + in.Batch.ID + ": state unavailable."
	}
	return v
}

// pricedAdapters drops adapters with no positive cost so the JSON per_adapter
// breakdown matches the text "Est. API cost:" line, which skips amount <= 0
// entries (formatSpendLine). An unpriced adapter (e.g. gemini, CostUSD==0) is
// recorded as a zero entry by the rollup but carries no cost data; it is
// surfaced via unpriced_runs, not as a $0.00 attribution. Returns nil when
// nothing is priced, so omitempty drops the field entirely.
func pricedAdapters(in map[string]float64) map[string]float64 {
	var out map[string]float64
	for name, amount := range in {
		if amount <= 0 {
			continue
		}
		if out == nil {
			out = make(map[string]float64, len(in))
		}
		out[name] = amount
	}
	return out
}

func activeSummary(b batch.Batch, p batch.Progress) string {
	done, total := strconv.Itoa(p.DonePlans), strconv.Itoa(p.TotalPlans)
	if p.AllDone {
		return "Batch " + b.ID + " complete (" + done + "/" + total + " integrated)."
	}
	return "Batch " + b.ID + ": " + done + "/" + total + " plans integrated."
}
