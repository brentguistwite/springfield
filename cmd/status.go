package cmd

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
	"springfield/internal/features/workflow"
)

// NewStatusCommand shows Springfield work status, preferring new batch state over legacy.
func NewStatusCommand() *cobra.Command {
	var dir string
	var workID string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show status for the active Springfield batch or a specific work id.",
		Long:  "Show status for the active Springfield batch or a specific work id.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}
			root := loaded.RootDir

			w := cmd.OutOrStdout()

			// Prefer new batch state when no explicit legacy work id was requested.
			if workID == "" {
				run, hasRun, runErr := batch.ReadRun(root)
				if runErr != nil {
					return runErr
				}

				if hasRun && run.ActiveBatchID != "" {
					paths, pathErr := batch.NewPaths(root, run.ActiveBatchID)
					if pathErr == nil {
						b, readErr := batch.ReadBatch(paths)
						if readErr == nil {
							return printBatchStatus(w, b, run)
						}
					}
				}

				// No batch state — check for legacy.
				legacy, legacyErr := batch.DetectLegacyWork(root)
				if legacyErr != nil {
					return legacyErr
				}
				if legacy != nil {
					fmt.Fprintln(w, "Note: legacy work state detected. Run \"springfield plan\" to create a new batch.")
					workID = legacy.ID
				} else {
					return fmt.Errorf("no Springfield batch or work found — run \"springfield plan\" first")
				}
			}

			// Legacy path.
			runner, err := workflow.NewRuntimeRunner(root, exec.LookPath, nil)
			if err != nil {
				return err
			}

			status, err := runner.Status(root, workID)
			if err != nil {
				return err
			}

			fmt.Fprintf(w, "Work: %s\n", status.WorkID)
			fmt.Fprintf(w, "Title: %s\n", status.Title)
			fmt.Fprintf(w, "Split: %s\n", status.Split)
			fmt.Fprintf(w, "Status: %s\n", status.Status)
			fmt.Fprintln(w, "")
			fmt.Fprintln(w, "Workstreams:")
			for _, workstream := range status.Workstreams {
				fmt.Fprintf(w, "  %s  %s  %s\n", workstream.Name, workstream.Status, workstream.Title)
				if workstream.Error != "" {
					fmt.Fprintf(w, "    Error: %s\n", workstream.Error)
				}
				if workstream.EvidencePath != "" {
					fmt.Fprintf(w, "    Evidence: %s\n", workstream.EvidencePath)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	cmd.Flags().StringVar(&workID, "work", "", "Springfield work id (default: active batch or work)")
	return cmd
}

func bindWorkflowFlags(cmd *cobra.Command, dir *string, workID *string) {
	cmd.Flags().StringVar(dir, "dir", ".", "project root or nested path inside the Springfield project")
	cmd.Flags().StringVar(workID, "work", "", "Springfield work id (default: active work)")
}

func resolveWorkflowTarget(dir, workID string) (string, string, error) {
	loaded, err := config.LoadFrom(dir)
	if err != nil {
		return "", "", err
	}

	resolvedWorkID := workID
	if resolvedWorkID == "" {
		resolvedWorkID, err = workflow.CurrentWorkID(loaded.RootDir)
		if err != nil {
			return "", "", err
		}
	}

	return loaded.RootDir, resolvedWorkID, nil
}

func printBatchStatus(w io.Writer, b batch.Batch, run batch.Run) error {
	fmt.Fprintf(w, "Batch: %s\n", b.ID)
	fmt.Fprintf(w, "Title: %s\n", b.Title)
	fmt.Fprintf(w, "Integration: %s\n", b.IntegrationMode)
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
