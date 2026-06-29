package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/core/lock"
	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/cost"
	"springfield/internal/features/execution"
	"springfield/internal/features/statusview"
)

// NewStatusCommand shows status for the active Springfield batch.
func NewStatusCommand() *cobra.Command {
	var dir string
	var jsonOut bool

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

			run, hasRun, err := batch.ReadRun(root)
			if err != nil {
				return err
			}
			if !hasRun || run.ActiveBatchID == "" {
				if jsonOut {
					return emitStatusJSON(cmd.OutOrStdout(), statusview.Idle())
				}
				return printPlanRegistry(cmd.OutOrStdout(), root)
			}

			paths, err := batch.NewPaths(root, run.ActiveBatchID)
			if err != nil {
				return err
			}
			b, err := batch.ReadBatch(paths)
			if err != nil {
				if batch.IsMissingBatchError(err) {
					if jsonOut {
						return emitStatusJSON(cmd.OutOrStdout(), statusview.Orphan(run))
					}
					printOrphanStatus(cmd.OutOrStdout(), run)
					return nil
				}
				return err
			}

			var state *conductor.State
			var units []conductor.PlanUnit
			project, loadErr := conductor.LoadProjectRaw(root)
			if loadErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "[warn] could not load project state: %v; progress rollup will be limited.\n", loadErr)
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

			if jsonOut {
				rollup, rollupErr := cost.ComputeRollup(root, b.ID)
				effectiveFatalError := ""
				if run.FatalError != "" && batchHasFailedPlan(b, state) {
					effectiveFatalError = run.FatalError
				}
				in := statusview.ActiveInput{
					Batch:      b,
					Run:        run,
					State:      state,
					Units:      units,
					Rollup:     rollup,
					HasRollup:  rollupErr == nil && rollup.Iterations > 0,
					FatalError: effectiveFatalError,
					Live:       live,
				}
				return emitStatusJSON(cmd.OutOrStdout(), statusview.Active(in))
			}
			return printBatchStatus(cmd.OutOrStdout(), root, b, run, state, live)
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit machine-readable JSON (stable view-model for tooling)")
	return cmd
}

func printBatchStatus(w io.Writer, root string, b batch.Batch, run batch.Run, state *conductor.State, live bool) error {
	fmt.Fprintf(w, "Batch: %s\n", b.ID)
	fmt.Fprintf(w, "Title: %s\n", b.Title)

	if state != nil {
		printProgressBlock(w, b, state, live)
		printSpendLine(w, root, b.ID)
	}

	if run.CostCapped {
		fmt.Fprintln(w, "Status: cost-capped")
	}
	// The batch-level fatal error is a post-mortem of the plan that halted the
	// run. Once that plan has been recovered (no plan in the batch is failed
	// anymore), the error is stale — suppress it so it does not sit beside a
	// fresh "Next:" gate and confuse the operator (D1).
	if run.FatalError != "" && batchHasFailedPlan(b, state) {
		fmt.Fprintf(w, "Fatal error: %s\n", run.FatalError)
	}
	if len(run.LastRetry) > 0 {
		fmt.Fprintln(w, "Recent retries:")
		for _, r := range run.LastRetry {
			fmt.Fprintf(w, "  - %s\n", r)
		}
	}
	if len(b.PlanIDs) > 0 {
		fmt.Fprintln(w, "Plans:")
		for _, id := range b.PlanIDs {
			fmt.Fprintf(w, "  %s\n", id)
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
func printProgressBlock(w io.Writer, b batch.Batch, state *conductor.State, live bool) {
	p := batch.ComputeProgress(b, state)
	fmt.Fprintf(w, "Plans: %d/%d integrated\n", p.DonePlans, p.TotalPlans)
	if p.AllDone {
		fmt.Fprintln(w, "Status: complete")
		return
	}

	// Group plans by canonical status. The grouping is TOTAL over the
	// statusview enum: every status the JSON view-model can emit per plan has a
	// home here, so a plan classified by JSON is never silently dropped from the
	// text surface (failed/needs-human/done used to fall through). merged plans
	// are already accounted for in the "X/Y integrated" line above. Because live
	// is batch-level, running and stalled are mutually exclusive (all in-flight
	// plans are running when a process owns the lock, stalled when none does).
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
		fmt.Fprintf(w, "Current: %s (%s)\n", strings.Join(running, ", "), label)
	case len(stalled) > 0:
		fmt.Fprintf(w, "Stalled: %s (no running springfield process — run \"springfield recover\")\n", strings.Join(stalled, ", "))
	}
	if len(failed) > 0 {
		fmt.Fprintf(w, "Failed: %s\n", strings.Join(failed, ", "))
	}
	if len(needsHuman) > 0 {
		fmt.Fprintf(w, "Needs human: %s\n", strings.Join(needsHuman, ", "))
	}
	if len(done) > 0 {
		fmt.Fprintf(w, "Done (not integrated): %s\n", strings.Join(done, ", "))
	}
	// "Next:" hints at what the queue runs next. It is meaningful only when the
	// queue can actually advance: when something is running (the next plan is
	// genuinely up after it), or when nothing is blocking. When the batch is
	// blocked — a stalled/failed/needs-human plan with nothing running — the
	// queue does not advance until the operator intervenes, so suppress the hint
	// rather than imply forward progress the batch cannot make.
	blocked := len(stalled) > 0 || len(failed) > 0 || len(needsHuman) > 0
	if len(pending) > 0 && (len(running) > 0 || !blocked) {
		fmt.Fprintf(w, "Next: %s\n", pending[0])
	}
}

// batchHasFailedPlan reports whether any plan in the batch is still in a
// halting state per the conductor snapshot. It gates whether the batch-level
// fatal error is still relevant: a sequential batch halts on the plan it
// leaves in StatusFailed OR StatusNeedsHuman (both set FatalError in run.json
// per cmd/start.go), so as long as either remains the error is still
// operator-actionable. Once the plan is recovered (e.g. via "springfield
// recover --plan X" or "--mark-completed") no halting plan remains and the
// now-stale error is dropped. When state is nil the snapshot is unavailable,
// so the error cannot be proven stale and is kept.
func batchHasFailedPlan(b batch.Batch, state *conductor.State) bool {
	if state == nil {
		return true
	}
	for _, id := range b.PlanIDs {
		ps, ok := state.Plans[id]
		if !ok || ps == nil {
			continue
		}
		if ps.Status == conductor.StatusFailed || ps.Status == conductor.StatusNeedsHuman {
			return true
		}
	}
	return false
}

// printSpendLine emits a "Spend:" line summarizing per-adapter cost rolled
// up from the live evidence directories. When ComputeRollup returns no
// iterations (fresh batch, no cost.json files yet), the line is omitted
// — there is nothing to display, not "Spend: $0.00".
func printSpendLine(w io.Writer, root, batchID string) {
	r, err := cost.ComputeRollup(root, batchID)
	if err != nil || r.Iterations == 0 {
		return
	}
	fmt.Fprintln(w, formatSpendLine(r))
}

// formatTotalSpendLine renders the end-of-batch "Total spend:" line shown
// after Status: completed. Same structure as formatSpendLine but with the
// "Total spend:" label and an unpriced hint that names gemini as the most
// likely culprit (the only adapter without cost capture in v1).
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

	out := fmt.Sprintf("Total spend: $%.2f", r.TotalUSD)
	if len(parts) > 0 {
		out += " (" + strings.Join(parts, ", ") + ")"
	}
	if r.UnpricedRuns > 0 {
		out += fmt.Sprintf(" (%d unpriced — likely gemini)", r.UnpricedRuns)
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

// formatSpendLine renders the Spend: line. Per-adapter breakdown is sorted
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

	out := fmt.Sprintf("Spend: $%.2f", r.TotalUSD)
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
	fmt.Fprint(w, rendered)
	return nil
}

func printOrphanStatus(w io.Writer, run batch.Run) {
	fmt.Fprintf(w, "Batch: %s (orphaned — batch.json missing)\n", run.ActiveBatchID)
	if run.CostCapped {
		// Spend figure intentionally omitted: batch.json is gone so
		// ComputeRollup cannot resolve the evidence path. Operator must
		// run recover before resuming.
		fmt.Fprintln(w, "Status: cost-capped")
	}
	if run.FatalError != "" {
		fmt.Fprintf(w, "Fatal error: %s\n", run.FatalError)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run \"springfield recover\" to archive the orphan and clear state,")
	fmt.Fprintln(w, "then \"springfield plan\" to start fresh.")
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
