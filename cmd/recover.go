package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
)

// NewRecoverCommand handles an orphaned run.json whose batch.json has vanished.
func NewRecoverCommand() *cobra.Command {
	var (
		dir      string
		diagnose bool
	)

	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Archive an orphaned batch and clear run state.",
		Long: "Archive an orphaned batch and clear run state.\n\n" +
			"Use when \"springfield status\" reports a missing batch.json while run.json still\n" +
			"points at a batch id. --diagnose prints what Springfield can see without modifying state.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}
			root := loaded.RootDir
			w := cmd.OutOrStdout()

			run, hasRun, err := batch.ReadRun(root)
			if err != nil {
				return err
			}
			if !hasRun || run.ActiveBatchID == "" {
				fmt.Fprintln(w, "No run.json present — nothing to recover.")
				return nil
			}

			paths, err := batch.NewPaths(root, run.ActiveBatchID)
			if err != nil {
				return fmt.Errorf("resolve batch paths: %w", err)
			}

			// If batch.json is still present, the run is not orphaned.
			// Only ENOENT is treated as orphan; any other filesystem error
			// (permission, transient I/O) must fail closed so we never
			// destroy live state based on a degraded read.
			if _, statErr := os.Stat(paths.BatchPath()); statErr == nil {
				fmt.Fprintf(w, "Batch %q still has a live batch.json — nothing to recover.\n", run.ActiveBatchID)
				fmt.Fprintln(w, "Run \"springfield start\" to resume or \"springfield status\" to inspect.")
				return nil
			} else if !os.IsNotExist(statErr) {
				return fmt.Errorf("stat batch.json (refusing to recover on non-ENOENT error): %w", statErr)
			}

			if diagnose {
				return printDiagnosis(w, root, run, paths)
			}

			if err := batch.RecoverOrphan(root, run); err != nil {
				return fmt.Errorf("recover orphan: %w", err)
			}

			fmt.Fprintf(w, "Archived orphan batch %q and cleared run state.\n", run.ActiveBatchID)
			sourcePath := paths.SourcePath()
			if _, err := os.Stat(sourcePath); err == nil {
				fmt.Fprintf(w, "Source markdown survived at %s — you can re-plan with:\n  springfield plan --file %s\n", sourcePath, sourcePath)
			} else {
				fmt.Fprintln(w, "Source markdown is also gone. Re-plan from a fresh file or prompt:")
				fmt.Fprintln(w, "  springfield plan --file <path>")
				fmt.Fprintln(w, "  springfield plan --prompt \"...\"")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	cmd.Flags().BoolVar(&diagnose, "diagnose", false, "print what Springfield can see without modifying state")
	return cmd
}

func printDiagnosis(w io.Writer, root string, run batch.Run, paths batch.Paths) error {
	fmt.Fprintln(w, "Diagnosis:")
	fmt.Fprintf(w, "  run.json active_batch_id: %s\n", run.ActiveBatchID)
	fmt.Fprintf(w, "  run.json active_phase_idx: %d\n", run.ActivePhaseIdx)
	if run.FatalError != "" {
		fmt.Fprintf(w, "  run.json fatal_error: %s\n", run.FatalError)
	}
	fmt.Fprintf(w, "  plan dir:      %s\n", statHint(paths.PlanDir()))
	fmt.Fprintf(w, "  batch.json:    %s\n", statHint(paths.BatchPath()))
	fmt.Fprintf(w, "  source.md:     %s\n", statHint(paths.SourcePath()))

	archiveDir := batch.ArchiveDir(root)
	fmt.Fprintf(w, "  archive dir:   %s\n", statHint(archiveDir))
	if entries, err := os.ReadDir(archiveDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			fmt.Fprintf(w, "    - %s\n", filepath.Base(e.Name()))
		}
	}

	fmt.Fprintln(w, "\nTo archive as orphan + clear run: springfield recover")
	return nil
}

func statHint(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "MISSING (" + path + ")"
		}
		return "ERROR (" + err.Error() + ")"
	}
	if info.IsDir() {
		return "present (dir, " + path + ")"
	}
	return "present (file, " + path + ")"
}
