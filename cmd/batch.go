package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planmerge"
	"springfield/internal/features/cost"
)

// NewBatchCommand groups lifecycle operations on the active Springfield batch.
func NewBatchCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "batch",
		Short: "Manage the active Springfield batch.",
		Long: "Manage the active Springfield batch.\n\n" +
			"Subcommands operate on the batch currently recorded in run.json.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newBatchAbortCommand())
	return cmd
}

// newBatchAbortCommand archives the active batch and clears all batch state —
// the sanctioned "abort and start over" path.
func newBatchAbortCommand() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "abort",
		Short: "Archive the active batch and clear all batch state.",
		Long: "Archive the active batch and clear all batch state.\n\n" +
			"Sanctioned \"abort this batch and start over\" path: archives the active\n" +
			"batch to .springfield/archive/, clears run.json, and removes the batch's\n" +
			"plan units from the registry. Older non-batch plan units are left intact.\n\n" +
			"Refuses if there is no active batch, or if any plan in the batch is running.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}
			return runBatchAbort(cmd.OutOrStdout(), loaded.RootDir)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	return cmd
}

// runBatchAbort performs the archive-and-clear teardown of the active batch.
// It mirrors the destructive core of "plan --replace" (archive prior, clear
// run, drop the batch's plan units) minus the new-envelope compile. Plan units
// that do not belong to the aborted batch are left registered.
func runBatchAbort(w io.Writer, root string) error {
	run, hasRun, err := batch.ReadRun(root)
	if err != nil {
		return fmt.Errorf("read run state: %w", err)
	}
	if !hasRun || run.ActiveBatchID == "" {
		return fmt.Errorf("no active batch to abort (run.json records no active batch)")
	}

	paths, err := batch.NewPaths(root, run.ActiveBatchID)
	if err != nil {
		return fmt.Errorf("resolve batch paths: %w", err)
	}

	b, err := batch.ReadBatch(paths)
	if err != nil {
		if batch.IsMissingBatchError(err) {
			return fmt.Errorf(
				"active batch %q has no batch.json (orphaned); run \"springfield recover\" to clean it up",
				run.ActiveBatchID,
			)
		}
		return fmt.Errorf("read active batch: %w", err)
	}

	project, err := conductor.LoadProjectRaw(root)
	if err != nil {
		return fmt.Errorf("load conductor project: %w", err)
	}

	// Refuse if any plan in the batch is currently running. A running plan means
	// "springfield start" is active; tearing down state now would corrupt
	// control-plane state the runner expects to stay stable.
	for _, planID := range b.PlanIDs {
		if ps := project.State.Plans[planID]; ps != nil && ps.Status == conductor.StatusRunning {
			return fmt.Errorf(
				"plan %q is currently running (status=running); wait for it to finish or run \"springfield recover\" first",
				planID,
			)
		}
	}

	// Before archive: best-effort remove each plan's worktree. A leaked worktree
	// here would block a subsequent batch that reuses any of these plan IDs
	// ("fatal: '<path>' is already a working tree"). We use --force so a dirty
	// checkout cannot block the abort, and the call is best-effort because the
	// recorded WorktreePath may already be gone (e.g., a prior failed run, or
	// `git worktree prune` already cleaned it). Per-failure warnings preserve
	// visibility without aborting the teardown.
	git := planmerge.CLIGit{}
	for _, planID := range b.PlanIDs {
		ps := project.State.Plans[planID]
		if ps == nil || ps.WorktreePath == "" {
			continue
		}
		if err := git.WorktreeRemoveForce(root, ps.WorktreePath); err != nil {
			fmt.Fprintf(w, "[warn] removing worktree for plan %q (%s): %v — git worktree prune may be needed before the same plan ID is reused\n",
				planID, ps.WorktreePath, err)
		}
	}

	// Capture the rollup before archive removes the live evidence dirs;
	// without this, the aborted batch loses its historical cost signal even
	// though the spend already happened.
	var abortRollup *cost.Rollup
	if r, rollupErr := cost.ComputeRollup(root, b.ID); rollupErr == nil {
		abortRollup = &r
	}
	// Archive the batch, then clear run.json immediately so there is no window
	// where run.json points at the now-removed batch dir.
	if err := batch.ArchiveBatchNormalized(root, b, "aborted", abortRollup); err != nil {
		return fmt.Errorf("archive active batch: %w", err)
	}
	if err := batch.ClearRun(root); err != nil {
		// "springfield recover" alone will NOT clean up: it reads run.json and
		// no-ops when none exists. The fix is a fresh plan via the
		// springfield:plan skill, which compiles a new batch and rebuilds run.json.
		return fmt.Errorf("clear run after archive (invoke the springfield:plan skill to compile a new batch — \"springfield recover\" will no-op without run.json): %w", err)
	}

	// Remove only the aborted batch's plan units; older non-batch units stay.
	for _, planID := range b.PlanIDs {
		_ = project.RemovePlanUnit(planID) // best-effort; may not be registered
	}
	if err := project.SaveConfig(); err != nil {
		// At this point run.json is already cleared; stale plan-units remain
		// registered. The recovery is "springfield plan --replace --prd <new>",
		// which rebuilds the registry from the new envelope. "springfield recover"
		// no-ops without run.json.
		return fmt.Errorf("save execution config (re-run \"springfield plan --replace --prd ...\" to rebuild the registry — \"springfield recover\" will no-op without run.json): %w", err)
	}

	fmt.Fprintf(w, "Aborted batch %q: archived to .springfield/archive/ and cleared run state.\n", b.ID)
	fmt.Fprintln(w, "Invoke the springfield:plan skill to compile a new batch.")
	return nil
}
