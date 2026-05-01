package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
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
				fmt.Fprintln(cmd.OutOrStdout(), "No active Springfield batch. Run \"springfield plan\" to create one.")
				return nil
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
			return printBatchStatus(cmd.OutOrStdout(), b, run)
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	return cmd
}

func printBatchStatus(w io.Writer, b batch.Batch, run batch.Run) error {
	fmt.Fprintf(w, "Batch: %s\n", b.ID)
	fmt.Fprintf(w, "Title: %s\n", b.Title)
	fmt.Fprintf(w, "Phase: %d of %d\n", run.ActivePhaseIdx+1, len(b.Phases))
	if run.FatalError != "" {
		fmt.Fprintf(w, "Fatal error: %s\n", run.FatalError)
	}
	if len(run.LastRetry) > 0 {
		fmt.Fprintln(w, "Recent retries:")
		for _, r := range run.LastRetry {
			fmt.Fprintf(w, "  - %s\n", r)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Slices:")
	for _, s := range b.Slices {
		fmt.Fprintf(w, "  %s  %s  %s\n", s.ID, s.Status, s.Title)
		if s.Error != "" {
			fmt.Fprintf(w, "    Error: %s\n", s.Error)
		}
		if s.EvidencePath != "" {
			fmt.Fprintf(w, "    Evidence: %s\n", s.EvidencePath)
		}
	}
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
