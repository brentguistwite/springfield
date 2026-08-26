package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/core/lock"
	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/cost"
	"springfield/internal/features/execution"
	"springfield/internal/features/prd"
	"springfield/internal/features/statusview"
)

// NewStatusCommand shows status for the active Springfield batch.
func NewStatusCommand() *cobra.Command {
	var dir string
	var jsonOut bool
	var watch bool
	var planID string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show status for the active Springfield batch.",
		Long:  "Show status for the active Springfield batch.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}
			root := loaded.RootDir

			// --plan follows a single plan's LIVE agent events, streamed from the
			// trace pipeline (never the post-hoc evidence events.jsonl). It is a
			// distinct surface from the batch view, so it takes precedence over the
			// --watch batch redraw and --json. --watch is accepted alongside it as
			// the natural "keep streaming" phrasing but adds nothing: follow always
			// streams. Read-only, like every status surface.
			if planID != "" {
				return runFollow(cmd.OutOrStdout(), root, planID)
			}

			// --watch re-renders the active batch on an interval, polling the
			// control plane read-only through the SAME statusview.Poll projection
			// --json emits — never a second view. It takes precedence over --json.
			if watch {
				return runWatch(cmd.OutOrStdout(), root)
			}

			// --json emits the stable view-model. It is built by the single
			// read-only Poll projection so the JSON and watch surfaces can never
			// disagree about a batch's state.
			if jsonOut {
				v, err := statusview.Poll(root)
				if err != nil {
					return err
				}
				return emitStatusJSON(cmd.OutOrStdout(), v)
			}

			run, hasRun, err := batch.ReadRun(root)
			if err != nil {
				return err
			}
			if !hasRun || run.ActiveBatchID == "" {
				return printPlanRegistry(cmd.OutOrStdout(), root)
			}

			paths, err := batch.NewPaths(root, run.ActiveBatchID)
			if err != nil {
				return err
			}
			b, err := batch.ReadBatch(paths)
			if err != nil {
				if batch.IsMissingBatchError(err) {
					printOrphanStatus(cmd.OutOrStdout(), run)
					return nil
				}
				return err
			}

			var state *conductor.State
			var units []conductor.PlanUnit
			project, loadErr := conductor.LoadProjectRaw(root)
			if loadErr != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[warn] could not load project state: %v; progress rollup will be limited.\n", loadErr)
			} else {
				state = project.State
				units = project.Config.PlanUnits
			}

			// Read-only liveness probe: a confirmed holder (PID != 0) means a live
			// springfield process owns the control-plane lock, so in-flight plans
			// are genuinely running; otherwise a started-but-non-terminal plan is
			// stalled (owning process died without a terminal result). Shared by
			// the text and JSON paths so both agree on running vs stalled.
			held := lock.Inspect(root)
			live := held != nil && held.PID != 0

			return printBatchStatus(cmd.OutOrStdout(), root, b, run, state, live, statusview.LoadPlanPRDs(root, units))
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit machine-readable JSON (stable view-model for tooling)")
	cmd.Flags().BoolVar(&watch, "watch", false, "re-render the active batch on an interval until interrupted (read-only)")
	cmd.Flags().StringVar(&planID, "plan", "", "follow a single plan's live agent events, filtered from the batch trace (read-only)")
	return cmd
}

// watchInterval is the redraw cadence for `status --watch`. It is deliberately
// gentle: the runner persists progress continuously, so a couple seconds keeps
// the frame fresh without hammering the control-plane files.
const watchInterval = 2 * time.Second

// runWatch re-renders the active batch status until interrupted (Ctrl-C). Each
// tick polls the control plane read-only and redraws with a plain terminal
// clear-and-home escape — no third-party TUI dependency. When no batch is
// active it prints a one-line idle notice and returns (exit 0) rather than
// spinning a redraw loop on nothing.
func runWatch(w io.Writer, root string) error {
	// seenActive records whether this watch has ever observed the batch in the
	// active state. It gates the archived final frame: only a batch we actually
	// watched running should end with its terminal frame (the active->archived
	// transition). A COLD watch that opens straight onto an archived state — a
	// prior batch whose run cursor was long cleared, with a new batch not yet
	// started — has nothing it was watching, so it is idle, not "just finished".
	seenActive := false
	for {
		active, err := watchFrame(w, root, time.Now(), true, seenActive)
		if err != nil {
			return err
		}
		if active {
			seenActive = true
		} else {
			return nil
		}
		time.Sleep(watchInterval)
	}
}

// watchFrame polls once and writes a single watch frame to w, returning whether
// the batch is still active (the caller keeps looping while true). It is the
// unit the redraw loop and the read-only watch tests both drive.
//
// clear gates the leading screen-clear escape: the live loop passes true so each
// redraw replaces the last; tests pass false to keep the captured output
// diffable. Poll is read-only, so ticking never writes under .springfield/.
//
// seenActive reports whether the loop has already observed the batch active on a
// prior tick. It gates the archived final frame (see below); the caller flips it
// to true after the first active frame.
//
// The natural end of a WATCHED batch is the archived state: the runner clears
// the run cursor and Poll surfaces the just-finished batch from the archive.
// That frame carries full per-plan rows, so — when the loop actually watched the
// batch run (seenActive) — it is RENDERED once (not dismissed with a one-liner),
// so an operator watching a batch to completion sees the terminal result, then
// the loop exits (false). A COLD watch that opens straight onto an archived
// state (seenActive false: a prior batch's cursor was long cleared, no new batch
// started) was watching nothing, so it is treated as idle — the stated
// no-active-batch semantics — not "just finished". Idle (never started) and
// orphan (broken cursor) likewise have no batch frame to draw, so they print a
// one-line notice and exit.
func watchFrame(w io.Writer, root string, now time.Time, clear, seenActive bool) (bool, error) {
	v, err := statusview.Poll(root)
	if err != nil {
		return false, err
	}
	switch {
	case v.State == "active", v.State == "archived" && seenActive:
		if clear {
			// Home cursor + clear screen: plain ANSI, no dependency.
			_, _ = fmt.Fprint(w, "\033[H\033[2J")
		}
		_, _ = fmt.Fprint(w, statusview.Render(v, now))
		// Active keeps the loop alive; archived is the terminal frame, so stop.
		return v.State == "active", nil
	default:
		_, _ = fmt.Fprintln(w, watchIdleMessage(v))
		return false, nil
	}
}

// watchIdleMessage renders the one-line notice shown when there is no batch
// frame to draw, tailored to why: never-started (idle), a cold watch that opened
// onto a long-archived batch (also idle — nothing was being watched), or a
// broken cursor (orphan). A watched batch that reaches archived is rendered as a
// final frame by watchFrame, not routed here.
func watchIdleMessage(v statusview.View) string {
	switch v.State {
	case "orphan":
		return v.Summary + " Nothing to watch."
	default:
		return "No active batch to watch."
	}
}

// followInterval is the poll cadence for `status --plan <id>`. It matches the
// watch cadence: the runner appends trace events continuously, so a couple
// seconds keeps the stream fresh without hammering the log file.
const followInterval = 2 * time.Second

// runFollow streams a single plan's live agent events until the batch is no
// longer active (or the process is interrupted). It sources from the live
// agent-trace stream — the ONLY tail-able per-event source; per-slice evidence
// events.jsonl is written post-hoc and cannot be followed live — and renders
// only the selected plan's lines, so concurrent siblings never interleave.
//
// The plan id is validated up front against the active batch's known plans: an
// unknown id fails fast with a message naming the known ids (non-zero exit),
// rather than silently tailing a stream that will never match. Everything here
// is read-only against .springfield/.
func runFollow(w io.Writer, root, planID string) error {
	v, err := statusview.Poll(root)
	if err != nil {
		return err
	}
	if v.State != "active" || v.Batch == nil {
		return fmt.Errorf("no active batch to follow")
	}
	known := make([]string, 0, len(v.Plans))
	found := false
	for _, p := range v.Plans {
		known = append(known, p.ID)
		if p.ID == planID {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("unknown plan %q; known plans: %s", planID, strings.Join(known, ", "))
	}

	batchID := v.Batch.ID
	// A restart/resume of this batch rolls the trace over to a new timestamped
	// file; the follower resets its offset on that path change so the head of
	// the new stream is not skipped by a stale offset.
	var follower statusview.TraceFollower
	for {
		tracePath, ok, err := statusview.LatestTracePath(root, batchID)
		if err != nil {
			return err
		}
		if ok {
			if err := follower.Tail(w, tracePath, planID); err != nil {
				return err
			}
		}
		// Stop once the batch we began following leaves the active state. Re-Poll
		// is read-only, matching the watch loop.
		v, err := statusview.Poll(root)
		if err != nil {
			return err
		}
		if !followInScope(v, batchID) {
			// The followed batch is done, so its trace is closed and will not grow
			// again. Events can still have been appended between this iteration's
			// Tail above and this Poll (e.g. the plan's terminal exit event racing
			// the run-cursor clear). Drain that remainder once more so the stream's
			// final lines are never dropped — the same no-drop guarantee the
			// follower already provides across rollovers. LatestTracePath is pinned
			// to batchID, so a different batch becoming active never redirects this
			// final drain onto the wrong trace.
			if tracePath, ok, terr := statusview.LatestTracePath(root, batchID); terr == nil && ok {
				if err := follower.Tail(w, tracePath, planID); err != nil {
					return err
				}
			}
			return nil
		}
		time.Sleep(followInterval)
	}
}

// followInScope reports whether a follow loop should keep tailing: the batch it
// began following (batchID) must still be THE active batch. It stops the loop
// when that batch has left the active state — archived, idle, or orphan — and,
// crucially, when a DIFFERENT batch has become active. A new batch that starts
// after the followed one finishes must not silently capture the loop, which
// would otherwise tail the old, now-dead trace forever and never exit. A resume
// of the followed batch keeps the same id (state stays active while a plan is
// merely stalled), so the loop continues and the rollover handling swaps files.
func followInScope(v statusview.View, batchID string) bool {
	return v.State == "active" && v.Batch != nil && v.Batch.ID == batchID
}

func printBatchStatus(w io.Writer, root string, b batch.Batch, run batch.Run, state *conductor.State, live bool, prds map[string]prd.PRD) error {
	_, _ = fmt.Fprintf(w, "Batch: %s\n", b.ID)
	_, _ = fmt.Fprintf(w, "Title: %s\n", b.Title)

	if state != nil {
		printProgressBlock(w, b, state, live, prds)
		printSpendLine(w, root, b.ID)
	}

	if run.CostCapped {
		_, _ = fmt.Fprintln(w, "Status: cost-capped")
	}
	// The batch-level fatal error is a post-mortem of the plan that halted the
	// run. Once that plan has been recovered (no plan in the batch is failed
	// anymore), the error is stale — suppress it so it does not sit beside a
	// fresh "Next:" gate and confuse the operator (D1).
	if run.FatalError != "" && statusview.BatchHasFailedPlan(b, state) {
		_, _ = fmt.Fprintf(w, "Fatal error: %s\n", run.FatalError)
	}
	if len(run.LastRetry) > 0 {
		_, _ = fmt.Fprintln(w, "Recent retries:")
		for _, r := range run.LastRetry {
			_, _ = fmt.Fprintf(w, "  - %s\n", r)
		}
	}
	if len(b.PlanIDs) > 0 {
		_, _ = fmt.Fprintln(w, "Plans:")
		for _, id := range b.PlanIDs {
			_, _ = fmt.Fprintf(w, "  %s\n", id)
		}
	}
	return nil
}

// printProgressBlock emits the plan-centric progress lines (Plans/Current/Next/
// Stalled or Status: complete) used by both `springfield status` and the
// start-header. Per-plan classification routes through statusview.ComposeStatus
// — the SAME canonical classifier the JSON view-model uses — so the text and
// JSON surfaces cannot disagree about a plan's state (e.g. an interrupted plan
// on a dead process is stalled in both, never "Next" in one and "stalled" in
// the other). live reports whether a springfield process owns the control-plane
// lock; the start-header caller passes live=true (the running start owns it).
//
// prds carries each plan's persisted prd.json (keyed by plan ID) so the
// per-running-plan in-flight activity line derives through the SAME
// statusview.DeriveActivity projection the JSON view-model uses — the text and
// JSON surfaces cannot disagree about what a running plan is doing. A nil/absent
// entry simply yields no activity line (truthful silence).
func printProgressBlock(w io.Writer, b batch.Batch, state *conductor.State, live bool, prds map[string]prd.PRD) {
	p := batch.ComputeProgress(b, state)
	_, _ = fmt.Fprintf(w, "Plans: %d/%d integrated\n", p.DonePlans, p.TotalPlans)
	if p.AllDone {
		_, _ = fmt.Fprintln(w, "Status: complete")
		return
	}

	// Group plans by canonical status. The switch is exhaustive over the
	// statuses ComposeStatus can produce (which feeds it): every such status has
	// an explicit arm, so a plan classified by JSON is never silently dropped from
	// the text surface (failed/needs-human/done used to fall through). merged (and
	// the archive-only retained) is the one status with no line — it is counted in the "X/Y integrated" tally
	// above, so its arm is an explicit no-op. Because live is batch-level,
	// running and stalled are mutually exclusive (all in-flight plans are running
	// when a process owns the lock, stalled when none does).
	var running, stalled, pending, failed, needsHuman, done []string
	for _, id := range b.PlanIDs {
		var ps *conductor.PlanState
		if state != nil {
			ps = state.Plans[id]
		}
		switch statusview.ComposeStatus(ps, live) {
		case statusview.StatusRunning:
			running = append(running, id)
		case statusview.StatusStalled:
			stalled = append(stalled, id)
		case statusview.StatusPending:
			pending = append(pending, id)
		case statusview.StatusFailed:
			failed = append(failed, id)
		case statusview.StatusNeedsHuman:
			needsHuman = append(needsHuman, id)
		case statusview.StatusDone:
			done = append(done, id)
		case statusview.StatusMerged, statusview.StatusRetained:
			// Counted in the "X/Y integrated" line above; no per-plan line.
			// Both are reachable here: ComposeStatus (which feeds this switch)
			// returns StatusRetained for an integrated standalone (per-plan) plan
			// and StatusMerged for a consolidate-merged one.
		}
	}

	switch {
	case len(running) > 0:
		// Label sourced from statusview.ParallelInFlight — the SAME classifier
		// that filled the running slice above and the JSON view-model's
		// progress.parallel_in_flight — so the text and JSON surfaces report
		// parallelism identically (not p.ParallelInFlight, which keys off
		// ClassifyPlan and would drift from the running/stalled classification).
		label := "running"
		if statusview.ParallelInFlight(b, state, live) {
			label = "parallel"
		}
		_, _ = fmt.Fprintf(w, "Current: %s (%s)\n", strings.Join(running, ", "), label)
		// In-flight activity, one line per running plan that has a derivable
		// current-activity. Routed through statusview.DeriveActivity so the text
		// line and the JSON `activity` card are the same projection — never a
		// stale or divergent phase. A plan with no derivable activity (no PRD, no
		// written stamp) prints no line rather than an invented one.
		for _, id := range running {
			var ps *conductor.PlanState
			if state != nil {
				ps = state.Plans[id]
			}
			var plan *prd.PRD
			if p, ok := prds[id]; ok {
				plan = &p
			}
			if av := statusview.DeriveActivity(ps, live, plan); av != nil {
				_, _ = fmt.Fprintf(w, "  %s: %s\n", id, formatActivity(av))
			}
		}
	case len(stalled) > 0:
		_, _ = fmt.Fprintf(w, "Stalled: %s (no running springfield process — run \"springfield recover\")\n", strings.Join(stalled, ", "))
	}
	if len(failed) > 0 {
		_, _ = fmt.Fprintf(w, "Failed: %s\n", strings.Join(failed, ", "))
	}
	if len(needsHuman) > 0 {
		_, _ = fmt.Fprintf(w, "Needs human: %s\n", strings.Join(needsHuman, ", "))
	}
	if len(done) > 0 {
		_, _ = fmt.Fprintf(w, "Done (not integrated): %s\n", strings.Join(done, ", "))
	}
	// "Next:" hints at what the queue runs next. It is meaningful only when the
	// queue can actually advance: when something is running (the next plan is
	// genuinely up after it), or when nothing is blocking. When the batch is
	// blocked — a stalled/failed/needs-human plan with nothing running — the
	// queue does not advance until the operator intervenes, so suppress the hint
	// rather than imply forward progress the batch cannot make.
	// A done (completed-but-not-integrated) plan blocks the sequential queue too:
	// the scheduler stays on its phase until it integrates, so a pending sibling
	// is not actually next.
	blocked := len(stalled) > 0 || len(failed) > 0 || len(needsHuman) > 0 || len(done) > 0
	if len(pending) > 0 && (len(running) > 0 || !blocked) {
		_, _ = fmt.Fprintf(w, "Next: %s\n", pending[0])
	}
}

// formatActivity renders an in-flight ActivityView as a compact one-liner for
// the text status surface: the coarse phase, then the optional detail (the
// current story / human phrase), then the optional fine round counter. It reads
// the same structured fields a JSON consumer sees, so the two surfaces convey
// identical content — e.g. "implementing US-001 (round 3)", "reviewing (round
// 2)", or bare "merging". Round is shown only when positive (the gates omit it
// until a round has begun).
func formatActivity(av *statusview.ActivityView) string {
	out := av.Phase
	if av.Detail != "" {
		out += " " + av.Detail
	}
	if av.Round > 0 {
		out += fmt.Sprintf(" (round %d)", av.Round)
	}
	return out
}

// printSpendLine emits an "Est. API cost:" line summarizing per-adapter cost
// rolled up from the live evidence directories. When ComputeRollup returns no
// iterations (fresh batch, no cost.json files yet), the line is omitted
// — there is nothing to display, not "Est. API cost: $0.00".
func printSpendLine(w io.Writer, root, batchID string) {
	r, err := cost.ComputeRollup(root, batchID)
	if err != nil || r.Iterations == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, formatSpendLine(r))
}

// formatTotalSpendLine renders the end-of-batch "Est. API-equivalent cost:"
// line shown after Status: completed. Same structure as formatSpendLine but
// with the batch-total label, a neutral unpriced count (more than one adapter
// can lack a computable price — gemini has no cost capture, and opencode on
// a free model reports $0), and a trailing note that subscription usage
// carries no per-run charge.
func formatTotalSpendLine(r cost.Rollup) string {
	adapters := make([]string, 0, len(r.PerAdapter))
	for name, amount := range r.PerAdapter {
		if amount <= 0 {
			continue
		}
		adapters = append(adapters, name)
	}
	sort.Strings(adapters)

	var parts []string
	for _, name := range adapters {
		parts = append(parts, fmt.Sprintf("%s $%.2f", name, r.PerAdapter[name]))
	}

	out := fmt.Sprintf("Est. API-equivalent cost: $%.2f", r.TotalUSD)
	if len(parts) > 0 {
		out += " (" + strings.Join(parts, ", ") + ")"
	}
	if r.UnpricedRuns > 0 {
		out += fmt.Sprintf(" (%d unpriced)", r.UnpricedRuns)
	}
	if r.SkippedFiles > 0 {
		noun := "files"
		if r.SkippedFiles == 1 {
			noun = "file"
		}
		out += fmt.Sprintf(" (%d %s skipped — totals may under-count)", r.SkippedFiles, noun)
	}
	// This figure is what the tokens would cost at published API rates, not a
	// bill. Phrased conditionally so it's honest for both cases: subscription
	// users aren't charged for it; genuine API-billed users are.
	out += " — this is the API-rate cost; you're not charged for it if you're on a Claude/Codex subscription"
	return out
}

// formatSpendLine renders the Est. API cost: line. Per-adapter breakdown is sorted
// by adapter name for deterministic output. Adapters with zero cost are
// omitted from the parenthetical. When the rollup includes unpriced runs,
// "(N unpriced)" is appended. When the rollup encountered unreadable
// cost.json files, "(M files skipped — totals may under-count)" is
// appended so operators know the spend figure is best-effort.
func formatSpendLine(r cost.Rollup) string {
	adapters := make([]string, 0, len(r.PerAdapter))
	for name, amount := range r.PerAdapter {
		if amount <= 0 {
			continue
		}
		adapters = append(adapters, name)
	}
	sort.Strings(adapters)

	var parts []string
	for _, name := range adapters {
		parts = append(parts, fmt.Sprintf("%s $%.2f", name, r.PerAdapter[name]))
	}

	out := fmt.Sprintf("Est. API cost: $%.2f", r.TotalUSD)
	if len(parts) > 0 {
		out += " (" + strings.Join(parts, ", ") + ")"
	}
	if r.UnpricedRuns > 0 {
		out += fmt.Sprintf(" (%d unpriced)", r.UnpricedRuns)
	}
	if r.SkippedFiles > 0 {
		noun := "files"
		if r.SkippedFiles == 1 {
			noun = "file"
		}
		out += fmt.Sprintf(" (%d %s skipped — totals may under-count)", r.SkippedFiles, noun)
	}
	return out
}

func printPlanRegistry(w io.Writer, root string) error {
	rendered, err := execution.RenderRegistryStatus(root)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprint(w, rendered)
	return nil
}

func printOrphanStatus(w io.Writer, run batch.Run) {
	_, _ = fmt.Fprintf(w, "Batch: %s (orphaned — batch.json missing)\n", run.ActiveBatchID)
	if run.CostCapped {
		// Spend figure intentionally omitted: batch.json is gone so
		// ComputeRollup cannot resolve the evidence path. Operator must
		// run recover before resuming.
		_, _ = fmt.Fprintln(w, "Status: cost-capped")
	}
	if run.FatalError != "" {
		_, _ = fmt.Fprintf(w, "Fatal error: %s\n", run.FatalError)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Run \"springfield recover\" to archive the orphan and clear state,")
	_, _ = fmt.Fprintln(w, "then \"springfield plan\" to start fresh.")
}

// emitStatusJSON marshals the status view-model with a trailing newline so
// piped consumers get a clean line-terminated document.
func emitStatusJSON(w io.Writer, v statusview.View) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}
