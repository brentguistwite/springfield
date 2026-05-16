package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/cost"
	"springfield/internal/features/execution"
)

// NewStatusCommand shows status for the active Springfield batch.
func NewStatusCommand() *cobra.Command {
	var dir string

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
			project, loadErr := conductor.LoadProjectRaw(root)
			if loadErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "[warn] could not load project state: %v; progress rollup will be limited.\n", loadErr)
			} else {
				state = project.State
			}
			return printBatchStatus(cmd.OutOrStdout(), root, b, run, state)
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	return cmd
}

func printBatchStatus(w io.Writer, root string, b batch.Batch, run batch.Run, state *conductor.State) error {
	fmt.Fprintf(w, "Batch: %s\n", b.ID)
	fmt.Fprintf(w, "Title: %s\n", b.Title)

	if state != nil {
		printProgressBlock(w, b, state)
		printSpendLine(w, root, b.ID)
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

// printProgressBlock emits the plan-centric progress lines (Plans/Current/Next
// or Status: complete) used by both `springfield status` and the start-header.
func printProgressBlock(w io.Writer, b batch.Batch, state *conductor.State) {
	p := batch.ComputeProgress(b, state)
	fmt.Fprintf(w, "Plans: %d/%d integrated\n", p.DonePlans, p.TotalPlans)
	switch {
	case p.AllDone:
		fmt.Fprintln(w, "Status: complete")
	case len(p.InFlight) > 0:
		label := "running"
		if p.ParallelInFlight {
			label = "parallel"
		}
		fmt.Fprintf(w, "Current: %s (%s)\n", strings.Join(p.InFlight, ", "), label)
		if len(p.Pending) > 0 {
			fmt.Fprintf(w, "Next: %s\n", p.Pending[0])
		}
	case len(p.Pending) > 0:
		fmt.Fprintf(w, "Next: %s\n", p.Pending[0])
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
	return out
}

// formatSpendLine renders the Spend: line. Per-adapter breakdown is sorted
// by adapter name for deterministic output. Adapters with zero cost are
// omitted from the parenthetical. When the rollup includes unpriced runs,
// "(N unpriced)" is appended.
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
	if run.FatalError != "" {
		fmt.Fprintf(w, "Fatal error: %s\n", run.FatalError)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run \"springfield recover\" to archive the orphan and clear state,")
	fmt.Fprintln(w, "then \"springfield plan\" to start fresh.")
}
