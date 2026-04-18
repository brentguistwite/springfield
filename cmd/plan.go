package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
)

// NewPlanCommand compiles a Springfield batch from a caller-provided slice payload.
func NewPlanCommand() *cobra.Command {
	var dir string
	var slicesArg string
	var replace bool
	var appendMode bool

	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Compile a Springfield plan into a runnable batch.",
		Long: "Compile a Springfield plan from a caller-provided slice payload.\n\n" +
			"Use --slices <path> to read a JSON payload from a file, or --slices - to read from stdin.\n" +
			"The springfield:plan skill emits this payload. Run \"springfield start\" to execute.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if slicesArg == "" {
				return fmt.Errorf("--slices is required (path to JSON payload, or \"-\" for stdin)")
			}

			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}
			root := loaded.RootDir

			payload, err := readSlicePayload(cmd, slicesArg)
			if err != nil {
				return err
			}

			priorBatch, existingIDs, err := checkActiveBatch(root, replace, appendMode)
			if err != nil {
				return err
			}

			compiled, err := batch.Compile(batch.CompileInput{
				Title:       payload.Title,
				Source:      payload.Source,
				Slices:      payload.Slices,
				ExistingIDs: existingIDs,
			})
			if err != nil {
				return fmt.Errorf("compile batch: %w", err)
			}

			return persistCompiledBatch(cmd, root, compiled, priorBatch, appendMode)
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	cmd.Flags().StringVar(&slicesArg, "slices", "", "path to slice payload JSON, or \"-\" to read from stdin")
	cmd.Flags().BoolVar(&replace, "replace", false, "archive the current active batch and replace it with this one")
	cmd.Flags().BoolVar(&appendMode, "append", false, "add new slices to the end of the current active batch")

	return cmd
}

func readSlicePayload(cmd *cobra.Command, slicesArg string) (batch.SlicePayload, error) {
	var r io.Reader
	if slicesArg == "-" {
		r = cmd.InOrStdin()
	} else {
		f, err := os.Open(slicesArg)
		if err != nil {
			return batch.SlicePayload{}, fmt.Errorf("open slice payload: %w", err)
		}
		defer f.Close()
		r = f
	}
	return batch.ParseSlicePayload(r)
}

// checkActiveBatch enforces the "no concurrent running slice" guard and returns
// (priorBatchToArchive, existingIDs, err). priorBatch is non-nil only on --replace.
func checkActiveBatch(root string, replace, appendMode bool) (*batch.Batch, map[string]struct{}, error) {
	existingIDs := map[string]struct{}{}
	run, hasRun, err := batch.ReadRun(root)
	if err != nil {
		return nil, nil, err
	}
	if !hasRun || run.ActiveBatchID == "" {
		return nil, existingIDs, nil
	}
	existingIDs[run.ActiveBatchID] = struct{}{}

	activePaths, pathErr := batch.NewPaths(root, run.ActiveBatchID)
	if pathErr != nil {
		return nil, existingIDs, nil
	}
	activeBatch, readErr := batch.ReadBatch(activePaths)
	if readErr != nil {
		return nil, existingIDs, nil
	}
	if activeBatch.HasRunningSlice() {
		return nil, nil, fmt.Errorf("a slice is currently running in batch %q — wait for it to finish before replacing or appending", run.ActiveBatchID)
	}
	if !replace && !appendMode {
		return nil, nil, fmt.Errorf("active batch %q already exists\nUse --replace to archive it and start fresh, or --append to add slices to it", run.ActiveBatchID)
	}
	if replace {
		b := activeBatch
		return &b, existingIDs, nil
	}
	return nil, existingIDs, nil
}

// persistCompiledBatch writes the batch, updates run.json, archives prior batch,
// prints summary. Appends route through appendToBatch.
func persistCompiledBatch(cmd *cobra.Command, root string, compiled batch.CompileOutput, priorBatch *batch.Batch, appendMode bool) error {
	if appendMode {
		run, _, err := batch.ReadRun(root)
		if err != nil {
			return err
		}
		if err := appendToBatch(root, run.ActiveBatchID, compiled.Batch); err != nil {
			return err
		}
		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "Appended %d slice(s) to batch %q.\n", len(compiled.Batch.Slices), run.ActiveBatchID)
		return nil
	}

	paths, err := batch.NewPaths(root, compiled.Batch.ID)
	if err != nil {
		return err
	}
	if err := batch.WriteBatch(paths, compiled.Batch, compiled.Source); err != nil {
		return fmt.Errorf("write batch: %w", err)
	}

	newRun := batch.Run{ActiveBatchID: compiled.Batch.ID, ActivePhaseIdx: 0}
	if err := batch.WriteRun(root, newRun); err != nil {
		if rollbackPaths, perr := batch.NewPaths(root, compiled.Batch.ID); perr == nil {
			if rmErr := os.RemoveAll(rollbackPaths.PlanDir()); rmErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to roll back new batch dir %s: %v\n", rollbackPaths.PlanDir(), rmErr)
			}
		}
		return fmt.Errorf("write run state: %w", err)
	}

	if priorBatch != nil {
		if archiveErr := batch.ArchiveBatch(root, *priorBatch, "replaced"); archiveErr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: archive prior batch %q: %v\n", priorBatch.ID, archiveErr)
		}
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Batch: %s\n", compiled.Batch.ID)
	fmt.Fprintf(w, "Title: %s\n", compiled.Batch.Title)
	fmt.Fprintf(w, "Slices: %d\n", len(compiled.Batch.Slices))
	for _, s := range compiled.Batch.Slices {
		fmt.Fprintf(w, "  %s  %s\n", s.ID, s.Title)
	}
	fmt.Fprintln(w, "\nRun \"springfield start\" to execute.")
	return nil
}

func appendToBatch(root, activeBatchID string, newBatch batch.Batch) error {
	paths, err := batch.NewPaths(root, activeBatchID)
	if err != nil {
		return err
	}
	active, err := batch.ReadBatch(paths)
	if err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(active.Slices))
	for _, s := range active.Slices {
		seen[s.ID] = struct{}{}
	}

	appendedIDs := make([]string, 0, len(newBatch.Slices))
	for _, s := range newBatch.Slices {
		newID := batch.UniqueID(s.ID, seen)
		seen[newID] = struct{}{}
		s.ID = newID
		active.Slices = append(active.Slices, s)
		appendedIDs = append(appendedIDs, newID)
	}

	if len(active.Phases) > 0 && active.Phases[len(active.Phases)-1].Mode == batch.PhaseSerial {
		last := &active.Phases[len(active.Phases)-1]
		last.Slices = append(last.Slices, appendedIDs...)
	} else {
		active.Phases = append(active.Phases, batch.Phase{
			Mode:   batch.PhaseSerial,
			Slices: appendedIDs,
		})
	}

	source, _ := os.ReadFile(paths.SourcePath())
	return batch.WriteBatch(paths, active, string(source))
}
