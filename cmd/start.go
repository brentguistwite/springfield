package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
	"springfield/internal/features/execution"
	"springfield/internal/features/workflow"
)

// NewStartCommand runs the active Springfield batch from its saved progress.
func NewStartCommand() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Execute the active Springfield batch for the current project from its saved progress.",
		Long:  "Execute the active Springfield batch for the current project from its saved progress.\n\nRun \"springfield plan\" first to compile a batch.",
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
				if batch.IsMissingBatchError(err) {
					if recoverErr := batch.RecoverOrphan(root, run); recoverErr != nil {
						return fmt.Errorf("orphan cleanup: %w", recoverErr)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "No active batch: prior run %q was orphaned and has been archived.\nRun \"springfield plan\" to start fresh.\n", run.ActiveBatchID)
					return nil
				}
				return fmt.Errorf("read active batch: %w", err)
			}

			// Tee Springfield's own stderr into a persistent log so warnings
			// are visible interactively and durable for post-mortem.
			logPath, closeLog, logErr := openBatchLog(cmd, root, b.ID)
			if logErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to open log file: %v\n", logErr)
			} else {
				defer closeLog()
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Batch: %s\n", b.ID)
			fmt.Fprintf(w, "Title: %s\n", b.Title)
			fmt.Fprintf(w, "Phase: %d of %d\n", run.ActivePhaseIdx+1, len(b.Phases))
			if logPath != "" {
				fmt.Fprintf(w, "Log: %s\n", logPath)
			}

			result, execErr := runBatch(root, run, b, w)

			run.LastCheckpoint = time.Now().UTC()
			if result.Error != "" {
				if !result.RunStateCleared {
					run.FatalError = result.Error
					if writeErr := batch.WriteRun(root, run); writeErr != nil {
						fmt.Fprintf(w, "Status: failed\n")
						fmt.Fprintf(w, "Error: %s\n", result.Error)
						return fmt.Errorf("batch %s failed; additionally failed to persist run state: %w", b.ID, writeErr)
					}
				}
				fmt.Fprintf(w, "Status: failed\n")
				fmt.Fprintf(w, "Error: %s\n", result.Error)
				if execErr != nil {
					return execErr
				}
				return fmt.Errorf("batch %s failed", b.ID)
			}

			// Atomic archive write is durable before we clear the cursor, so
			// archive first. If the process dies between archive-rename and
			// ClearRun, the next start sees run.json pointing at an already-
			// archived id and RecoverOrphan handles it idempotently.
			if archiveErr := batch.ArchiveBatchNormalized(root, b, "completed"); archiveErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: archive completed batch %q: %v\n", b.ID, archiveErr)
			}
			if clearErr := batch.ClearRun(root); clearErr != nil {
				return fmt.Errorf("clear run state after completion: %w", clearErr)
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
	// RunStateCleared is true when runBatch has already archived+cleared the
	// run cursor on an unrecoverable path (e.g. tamper detection). The caller
	// must not re-write run.json, or the cleared cursor gets stranded again.
	RunStateCleared bool
}

func runBatch(root string, run batch.Run, b batch.Batch, progress io.Writer) (BatchRunResult, error) {
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

		fmt.Fprintf(progress, "  slice %s start — %s\n", s.ID, s.Title)

		s.Status = batch.SliceRunning
		if err := batch.UpdateBatchSlice(batchPaths, s); err != nil {
			return BatchRunResult{Error: err.Error()}, err
		}

		// Snapshot the entire Springfield control plane before the agent
		// runs: batch.json, run.json, source.md. The agent is not expected
		// to touch any of them; any byte-level difference is tamper.
		snap, snapErr := snapshotControlPlane(root, batchPaths)
		if snapErr != nil {
			return BatchRunResult{Error: fmt.Sprintf("snapshot control plane: %v", snapErr)}, snapErr
		}

		report, runErr := runner.Executor.Run(root, sliceToExecutionWork(b, s))

		if tamperErr := detectAndRecoverTamper(root, batchPaths, snap); tamperErr != nil {
			return BatchRunResult{Error: tamperErr.Error(), RunStateCleared: true}, tamperErr
		}

		if runErr != nil || report.Status == "failed" {
			s.Status = batch.SliceFailed
			s.Error = report.Error
			if runErr != nil && s.Error == "" {
				s.Error = runErr.Error()
			}
			if err := batch.UpdateBatchSlice(batchPaths, s); err != nil {
				return BatchRunResult{Error: s.Error}, fmt.Errorf("%s; also failed to persist slice status: %w", s.Error, err)
			}
			fmt.Fprintf(progress, "  slice %s failed — %s\n", s.ID, s.Error)
			return BatchRunResult{Error: s.Error}, runErr
		}

		s.Status = batch.SliceDone
		if err := batch.UpdateBatchSlice(batchPaths, s); err != nil {
			return BatchRunResult{Error: err.Error()}, err
		}
		fmt.Fprintf(progress, "  slice %s done\n", s.ID)
	}

	return BatchRunResult{Status: "completed"}, nil
}

// controlPlaneSnapshot captures the bytes of every Springfield-owned file
// under .springfield/plans/<id>/ plus run.json, taken between Springfield's
// pre-agent write and the agent's execution. Springfield does not touch any
// of these files while the agent is running, so any post-agent byte-level
// difference is tamper.
type controlPlaneSnapshot struct {
	batchBytes  []byte
	runBytes    []byte
	sourceBytes []byte
}

func snapshotControlPlane(root string, paths batch.Paths) (controlPlaneSnapshot, error) {
	batchBytes, err := os.ReadFile(paths.BatchPath())
	if err != nil {
		return controlPlaneSnapshot{}, fmt.Errorf("read batch.json: %w", err)
	}
	runBytes, err := os.ReadFile(batch.RunPath(root))
	if err != nil {
		return controlPlaneSnapshot{}, fmt.Errorf("read run.json: %w", err)
	}
	sourceBytes, err := readOptional(paths.SourcePath())
	if err != nil {
		return controlPlaneSnapshot{}, fmt.Errorf("read source.md: %w", err)
	}
	return controlPlaneSnapshot{batchBytes: batchBytes, runBytes: runBytes, sourceBytes: sourceBytes}, nil
}

func readOptional(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// detectAndRecoverTamper enforces the Workstream-B invariant that agents must
// not modify Springfield control-plane state. On any byte-level difference,
// the snapshot is restored, the batch is archived as "state-tampered", and
// run.json is cleared.
func detectAndRecoverTamper(root string, paths batch.Paths, snap controlPlaneSnapshot) error {
	reason := compareControlPlane(root, paths, snap)
	if reason == "" {
		return nil
	}

	var restoreErr, archiveErr error
	if err := batch.RestoreBatchFromSnapshot(paths, snap.batchBytes); err != nil {
		restoreErr = err
	} else {
		// Restore run.json + source.md too so recover/archive work from a
		// coherent pre-agent state.
		_ = os.WriteFile(batch.RunPath(root), snap.runBytes, 0o644)
		if snap.sourceBytes != nil {
			_ = os.WriteFile(paths.SourcePath(), snap.sourceBytes, 0o644)
		}
		var restored batch.Batch
		if err := json.Unmarshal(snap.batchBytes, &restored); err == nil {
			if err := batch.ArchiveBatchNormalized(root, restored, "state-tampered"); err != nil {
				archiveErr = err
			}
		}
	}
	_ = batch.ClearRun(root)

	msg := fmt.Sprintf("state tampered by agent (%s)", reason)
	if restoreErr != nil {
		msg += fmt.Sprintf("; restore failed: %v", restoreErr)
	}
	if archiveErr != nil {
		msg += fmt.Sprintf("; archive failed: %v", archiveErr)
	}
	return fmt.Errorf("%s", msg)
}

// compareControlPlane returns "" when on-disk state matches the snapshot
// byte-for-byte; otherwise a short label naming which file diverged.
func compareControlPlane(root string, paths batch.Paths, snap controlPlaneSnapshot) string {
	batchNow, err := os.ReadFile(paths.BatchPath())
	if err != nil {
		return "batch.json missing or unreadable"
	}
	if !bytes.Equal(batchNow, snap.batchBytes) {
		return "batch.json bytes changed"
	}
	runNow, err := os.ReadFile(batch.RunPath(root))
	if err != nil {
		return "run.json missing or unreadable"
	}
	if !bytes.Equal(runNow, snap.runBytes) {
		return "run.json bytes changed"
	}
	sourceNow, err := readOptional(paths.SourcePath())
	if err != nil {
		return "source.md unreadable"
	}
	if !bytes.Equal(sourceNow, snap.sourceBytes) {
		return "source.md bytes changed"
	}
	return ""
}

// openBatchLog tees Springfield's cobra stdout+stderr into a persistent log
// under .springfield/logs/. The terminal still receives both streams.
func openBatchLog(cmd *cobra.Command, root, batchID string) (string, func(), error) {
	logsDir := filepath.Join(root, ".springfield", "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return "", nil, err
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	logPath := filepath.Join(logsDir, fmt.Sprintf("%s-%s.log", batchID, ts))
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", nil, err
	}
	cmd.SetOut(io.MultiWriter(cmd.OutOrStdout(), f))
	cmd.SetErr(io.MultiWriter(cmd.ErrOrStderr(), f))
	closer := func() { _ = f.Close() }
	return logPath, closer, nil
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
