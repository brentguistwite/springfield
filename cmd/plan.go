package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
)

// NewPlanCommand compiles a Springfield batch from a plan file or prompt.
func NewPlanCommand() *cobra.Command {
	var dir string
	var file string
	var prompt string
	var replace bool
	var appendMode bool

	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Compile a Springfield plan into a runnable batch.",
		Long:  "Compile a Springfield plan from a markdown file or prompt into a runnable batch.\n\nUse --file to compile from an existing plan.md or --prompt for a direct request.\nRun \"springfield start\" to execute the compiled batch.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}
			root := loaded.RootDir

			source, kind, err := resolvePlanSource(file, prompt)
			if err != nil {
				return err
			}

			title := deriveTitleFromSource(source, file)

			run, hasRun, err := batch.ReadRun(root)
			if err != nil {
				return err
			}

			var priorBatch *batch.Batch
			if hasRun && run.ActiveBatchID != "" {
				activePaths, pathErr := batch.NewPaths(root, run.ActiveBatchID)
				if pathErr == nil {
					activeBatch, readErr := batch.ReadBatch(activePaths)
					if readErr == nil && activeBatch.HasRunningSlice() {
						return fmt.Errorf("a slice is currently running in batch %q — wait for it to finish before replacing or appending", run.ActiveBatchID)
					}
					if readErr == nil {
						if !replace && !appendMode {
							return fmt.Errorf("active batch %q already exists\nUse --replace to archive it and start fresh, or --append to add slices to it", run.ActiveBatchID)
						}
						if replace {
							// Defer archive until after new batch is fully written.
							b := activeBatch
							priorBatch = &b
						}
					}
				}
			}

			existingIDs := map[string]struct{}{}
			if hasRun && run.ActiveBatchID != "" {
				existingIDs[run.ActiveBatchID] = struct{}{}
			}

			compiled, err := batch.Compile(batch.CompileInput{
				Title:       title,
				Source:      source,
				Kind:        kind,
				ExistingIDs: existingIDs,
			})
			if err != nil {
				return fmt.Errorf("compile batch: %w", err)
			}

			if appendMode && hasRun && run.ActiveBatchID != "" {
				return appendToBatch(root, run.ActiveBatchID, compiled.Batch)
			}

			paths, err := batch.NewPaths(root, compiled.Batch.ID)
			if err != nil {
				return err
			}
			if err := batch.WriteBatch(paths, compiled.Batch, compiled.Source); err != nil {
				return fmt.Errorf("write batch: %w", err)
			}

			newRun := batch.Run{
				ActiveBatchID:  compiled.Batch.ID,
				ActivePhaseIdx: 0,
			}
			if err := batch.WriteRun(root, newRun); err != nil {
				// Roll back new batch dir so we don't leak an orphan.
				if rollbackPaths, perr := batch.NewPaths(root, compiled.Batch.ID); perr == nil {
					if rmErr := os.RemoveAll(rollbackPaths.PlanDir()); rmErr != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to roll back new batch dir %s: %v\n", rollbackPaths.PlanDir(), rmErr)
					}
				}
				return fmt.Errorf("write run state: %w", err)
			}

			// New batch is committed — now archive the prior one (best-effort is OK;
			// the invariant we protect is that run.json always points at a real batch).
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
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	cmd.Flags().StringVar(&file, "file", "", "path to an existing plan markdown file")
	cmd.Flags().StringVar(&prompt, "prompt", "", "direct work request (used when --file is not provided)")
	cmd.Flags().BoolVar(&replace, "replace", false, "archive the current active batch and replace it with this one")
	cmd.Flags().BoolVar(&appendMode, "append", false, "add new slices to the end of the current active batch")

	return cmd
}

func resolvePlanSource(file, prompt string) (string, batch.SourceKind, error) {
	if file != "" && prompt != "" {
		return "", "", fmt.Errorf("provide --file or --prompt, not both")
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", "", fmt.Errorf("read plan file %s: %w", file, err)
		}
		return string(data), batch.SourceFile, nil
	}
	if prompt != "" {
		return strings.TrimSpace(prompt), batch.SourcePrompt, nil
	}
	// Interactive prompt from stdin.
	fmt.Fprint(os.Stderr, "Enter your work request (Ctrl-D to submit):\n")
	var sb strings.Builder
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		sb.WriteString(scanner.Text())
		sb.WriteString("\n")
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return "", "", fmt.Errorf("no work request provided")
	}
	return text, batch.SourcePrompt, nil
}

func deriveTitleFromSource(source, file string) string {
	if file != "" {
		for _, line := range strings.Split(source, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "# ") {
				return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			}
		}

		// Derive from filename slug (strip extension and path).
		base := file
		for i := len(base) - 1; i >= 0; i-- {
			if base[i] == '/' || base[i] == '\\' {
				base = base[i+1:]
				break
			}
		}
		if dot := strings.LastIndex(base, "."); dot > 0 {
			base = base[:dot]
		}
		return base
	}
	// Derive from first non-empty line of the prompt.
	for _, line := range strings.Split(source, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			if len(t) > 60 {
				t = t[:60]
			}
			return t
		}
	}
	return "Springfield batch"
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

	// Flatten appended slices into the trailing serial phase (creating one if needed).
	// Compile currently emits only a single serial phase per batch, so preserving the
	// incoming phase shape is dead code today.
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
