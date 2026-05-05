package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
	"springfield/internal/features/execution"
)

// NewRecoverCommand handles plan-failure recovery (--plan) and orphan-batch recovery.
func NewRecoverCommand() *cobra.Command {
	var (
		dir      string
		diagnose bool
		planID   string
	)

	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Recover from a failed plan or an orphaned batch.",
		Long: "Recover from a failed plan or an orphaned batch.\n\n" +
			"Without --plan: archive an orphaned batch (run.json with missing batch.json)\n" +
			"and clear run state.\n\n" +
			"With --plan <id>: diagnose or recover a failed/interrupted plan.\n" +
			"Use --diagnose to inspect without modifying state.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}
			root := loaded.RootDir
			w := cmd.OutOrStdout()

			if planID != "" {
				return runPlanRecover(w, root, planID, diagnose)
			}

			return runOrphanRecover(w, root, diagnose)
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	cmd.Flags().BoolVar(&diagnose, "diagnose", false, "print what Springfield can see without modifying state")
	cmd.Flags().StringVar(&planID, "plan", "", "plan ID to diagnose or recover (omit for orphan-batch recovery)")
	return cmd
}

func runPlanRecover(w io.Writer, root, planID string, diagnoseOnly bool) error {
	diag, err := execution.DiagnosePlan(root, planID)
	if err != nil {
		return err
	}

	if diagnoseOnly {
		fmt.Fprint(w, diag.Render())
		return nil
	}

	if len(diag.AvailableActions) == 0 {
		fmt.Fprint(w, diag.Render())
		fmt.Fprintln(w, "No automatic recovery actions available for this plan state.")
		return nil
	}

	action := diag.AvailableActions[0].Action
	rec, err := execution.RecoverPlan(root, planID, action)
	if err != nil {
		return fmt.Errorf("recover plan %q: %w", planID, err)
	}

	fmt.Fprintf(w, "Recovered plan %q: %s\n", planID, rec.Reason)
	fmt.Fprintln(w, "Run \"springfield start\" to continue.")
	return nil
}

func runOrphanRecover(w io.Writer, root string, diagnoseOnly bool) error {
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

	if diagnoseOnly {
		return printOrphanDiagnosis(w, root, run, paths)
	}

	if err := batch.RecoverOrphan(root, run); err != nil {
		return fmt.Errorf("recover orphan: %w", err)
	}

	fmt.Fprintf(w, "Archived orphan batch %q and cleared run state.\n", run.ActiveBatchID)
	sourcePath := paths.SourcePath()
	if _, err := os.Stat(sourcePath); err == nil {
		fmt.Fprintf(w, "Source markdown survived at %s — invoke the springfield:plan skill to re-slice and re-plan.\n", sourcePath)
	} else {
		fmt.Fprintln(w, "Source markdown is also gone. Invoke the springfield:plan skill to create a new batch.")
	}
	return nil
}

func printOrphanDiagnosis(w io.Writer, root string, run batch.Run, paths batch.Paths) error {
	fmt.Fprintln(w, "Diagnosis:")
	fmt.Fprintf(w, "  run.json active_batch_id: %s\n", run.ActiveBatchID)
	fmt.Fprintf(w, "  run.json active_phase_idx: %d\n", run.ActivePhaseIdx)
	if run.FatalError != "" {
		fmt.Fprintf(w, "  run.json fatal_error: %s\n", run.FatalError)
	}
	fmt.Fprintf(w, "  plan dir:      %s\n", statHint(paths.PlanDir()))
	fmt.Fprintf(w, "  batch.json:    %s\n", statHint(paths.BatchPath()))
	fmt.Fprintf(w, "  source.md:     %s\n", statHint(paths.SourcePath()))
	evidenceDir := filepath.Join(paths.PlanDir(), "evidence")
	fmt.Fprintf(w, "  evidence dir:  %s\n", statHint(evidenceDir))
	if entries, err := os.ReadDir(evidenceDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			fmt.Fprintf(w, "    - %s\n", filepath.Join(evidenceDir, e.Name()))
		}
	}

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
