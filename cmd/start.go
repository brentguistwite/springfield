package cmd

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
	"springfield/internal/features/execution"
	"springfield/internal/features/workflow"
)

// NewStartCommand runs the active Springfield batch from its saved cursor.
func NewStartCommand() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Execute the active Springfield batch from its saved cursor.",
		Long:  "Execute the active Springfield batch from its saved cursor.\n\nRun \"springfield plan\" first to compile a batch.",
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
				return fmt.Errorf("no Springfield plan found for this repo — run \"springfield plan\" first")
			}

			paths, err := batch.NewPaths(root, run.ActiveBatchID)
			if err != nil {
				return fmt.Errorf("resolve batch paths: %w", err)
			}

			b, err := batch.ReadBatch(paths)
			if err != nil {
				return fmt.Errorf("read active batch: %w", err)
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Batch: %s\n", b.ID)
			fmt.Fprintf(w, "Title: %s\n", b.Title)
			fmt.Fprintf(w, "Phase: %d of %d\n", run.ActivePhaseIdx+1, len(b.Phases))

			result, execErr := runBatch(root, run, b)

			run.LastCheckpoint = time.Now().UTC()
			if result.Error != "" {
				run.LastError = result.Error
				if writeErr := batch.WriteRun(root, run); writeErr != nil {
					fmt.Fprintf(w, "Status: failed\n")
					fmt.Fprintf(w, "Error: %s\n", result.Error)
					return fmt.Errorf("batch %s failed; additionally failed to persist run state: %w", b.ID, writeErr)
				}
				fmt.Fprintf(w, "Status: failed\n")
				fmt.Fprintf(w, "Error: %s\n", result.Error)
				if execErr != nil {
					return execErr
				}
				return fmt.Errorf("batch %s failed", b.ID)
			}

			// Clear the cursor first so a downstream archive failure doesn't strand
			// run.json pointing at a moved batch directory.
			if clearErr := batch.ClearRun(root); clearErr != nil {
				return fmt.Errorf("clear run state after completion: %w", clearErr)
			}
			if archiveErr := batch.ArchiveBatch(root, b, "completed"); archiveErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: archive completed batch %q: %v\n", b.ID, archiveErr)
			}

			fmt.Fprintf(w, "Status: completed\n")
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	return cmd
}

// BatchRunResult summarizes the outcome of running a batch.
type BatchRunResult struct {
	Status string
	Error  string
}

func runBatch(root string, run batch.Run, b batch.Batch) (BatchRunResult, error) {
	runner, err := workflow.NewRuntimeRunner(root, exec.LookPath, nil)
	if err != nil {
		return BatchRunResult{Error: err.Error()}, err
	}

	phase, ok := b.ActivePhase(run.ActivePhaseIdx)
	if !ok {
		return BatchRunResult{Status: "completed"}, nil
	}

	batchPaths, pathErr := batch.NewPaths(root, b.ID)
	if pathErr != nil {
		return BatchRunResult{Error: pathErr.Error()}, pathErr
	}

	for _, sliceID := range phase.Slices {
		s, found := b.SliceByID(sliceID)
		if !found {
			return BatchRunResult{Error: fmt.Sprintf("slice %q not found in batch", sliceID)}, nil
		}
		if s.Status == batch.SliceDone {
			continue
		}

		s.Status = batch.SliceRunning
		if err := batch.UpdateBatchSlice(batchPaths, s); err != nil {
			return BatchRunResult{Error: err.Error()}, err
		}

		report, runErr := runner.Executor.Run(root, sliceToExecutionWork(b, s))
		if runErr != nil || report.Status == "failed" {
			s.Status = batch.SliceFailed
			s.Error = report.Error
			if runErr != nil && s.Error == "" {
				s.Error = runErr.Error()
			}
			if err := batch.UpdateBatchSlice(batchPaths, s); err != nil {
				// Surface both errors; the slice failure is primary.
				return BatchRunResult{Error: s.Error}, fmt.Errorf("%s; also failed to persist slice status: %w", s.Error, err)
			}
			return BatchRunResult{Error: s.Error}, runErr
		}

		s.Status = batch.SliceDone
		if err := batch.UpdateBatchSlice(batchPaths, s); err != nil {
			return BatchRunResult{Error: err.Error()}, err
		}
	}

	return BatchRunResult{Status: "completed"}, nil
}

// sliceToExecutionWork converts a batch slice into an execution.Work for the runtime adapter.
func sliceToExecutionWork(b batch.Batch, s batch.Slice) execution.Work {
	return execution.Work{
		ID:    b.ID + "-" + s.ID,
		Title: s.Title,
		Split: "single",
		Workstreams: []execution.Workstream{
			{
				Name:    s.ID,
				Title:   s.Title,
				Summary: s.Summary,
			},
		},
	}
}
