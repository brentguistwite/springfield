package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
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

	// Archive the batch, then clear run.json immediately so there is no window
	// where run.json points at the now-removed batch dir.
	if err := batch.ArchiveBatchNormalized(root, b, "aborted"); err != nil {
		return fmt.Errorf("archive active batch: %w", err)
	}
	if err := batch.ClearRun(root); err != nil {
		return fmt.Errorf("clear run after archive (run \"springfield recover\" to clean up): %w", err)
	}

	// Remove only the aborted batch's plan units; older non-batch units stay.
	for _, planID := range b.PlanIDs {
		_ = project.RemovePlanUnit(planID) // best-effort; may not be registered
	}
	if err := project.SaveConfig(); err != nil {
		return fmt.Errorf("save execution config (run \"springfield recover\" to clean up): %w", err)
	}

	fmt.Fprintf(w, "Aborted batch %q: archived to .springfield/archive/ and cleared run state.\n", b.ID)
	fmt.Fprintln(w, "Invoke the springfield:plan skill to compile a new batch.")
	return nil
}
