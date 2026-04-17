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
				return fmt.Errorf("no Springfield batch found — run \"springfield plan\" first")
			}

			paths, err := batch.NewPaths(root, run.ActiveBatchID)
			if err != nil {
				return err
			}
			b, err := batch.ReadBatch(paths)
			if err != nil {
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
	if run.LastError != "" {
		fmt.Fprintf(w, "Last error: %s\n", run.LastError)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Slices:")
	for _, s := range b.Slices {
		fmt.Fprintf(w, "  %s  %s  %s\n", s.ID, s.Status, s.Title)
		if s.Error != "" {
			fmt.Fprintf(w, "    Error: %s\n", s.Error)
		}
	}
	return nil
}
