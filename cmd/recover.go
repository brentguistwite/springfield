package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planrun"
	"springfield/internal/features/execution"
)

// NewRecoverCommand handles plan-failure recovery (--plan) and orphan-batch recovery.
func NewRecoverCommand() *cobra.Command {
	var (
		dir           string
		diagnose      bool
		planID        string
		markCompleted bool
		acceptDrift   bool
		reset         bool
	)

	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Recover from a failed plan or an orphaned batch.",
		Long: "Recover from a failed plan or an orphaned batch.\n\n" +
			"Without --plan: archive an orphaned batch (run.json with missing batch.json)\n" +
			"and clear run state.\n\n" +
			"With --plan <id>: diagnose or recover a failed/interrupted plan.\n" +
			"Use --diagnose to inspect without modifying state.\n\n" +
			"With --plan <id> --mark-completed: flip a non-completed plan to completed\n" +
			"once every story in its prd.json passes, and queue the merge for the next\n" +
			"\"springfield start\". Rejected if any story is unpassed.\n\n" +
			"With --plan <id> --accept-drift: accept a deliberate input change (e.g. an\n" +
			"updated AGENTS.md or prd.json edit) that the digest flagged as drift —\n" +
			"record the current input digest and reset the plan to pending so the next\n" +
			"\"springfield start\" no longer refuses with preflight-input-drift.\n\n" +
			"With --plan <id> --reset: discard a prior attempt entirely — remove its\n" +
			"worktree, delete its springfield/<plan> branch, and reset the plan to a\n" +
			"clean first-run. Use this (not --accept-drift) when you want to start over\n" +
			"rather than resume the existing worktree.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}
			root := loaded.RootDir
			w := cmd.OutOrStdout()

			if reset {
				if planID == "" {
					return fmt.Errorf("--reset requires --plan <id>")
				}
				if diagnose || markCompleted || acceptDrift {
					return fmt.Errorf("--reset cannot be combined with --diagnose, --mark-completed, or --accept-drift")
				}
				return runPlanReset(w, root, planID)
			}

			if acceptDrift {
				if planID == "" {
					return fmt.Errorf("--accept-drift requires --plan <id>")
				}
				if diagnose {
					return fmt.Errorf("--accept-drift cannot be combined with --diagnose")
				}
				if markCompleted {
					return fmt.Errorf("--accept-drift cannot be combined with --mark-completed")
				}
				return runPlanAcceptDrift(w, root, planID)
			}

			if markCompleted {
				if planID == "" {
					return fmt.Errorf("--mark-completed requires --plan <id>")
				}
				if diagnose {
					return fmt.Errorf("--mark-completed cannot be combined with --diagnose")
				}
				return runPlanMarkCompleted(w, root, planID)
			}

			if planID != "" {
				return runPlanRecover(w, root, planID, diagnose)
			}

			return runOrphanRecover(w, root, diagnose)
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	cmd.Flags().BoolVar(&diagnose, "diagnose", false, "print what Springfield can see without modifying state")
	cmd.Flags().StringVar(&planID, "plan", "", "plan ID to diagnose or recover (omit for orphan-batch recovery)")
	cmd.Flags().BoolVar(&markCompleted, "mark-completed", false, "with --plan: mark a non-completed plan completed (requires all stories passing) and queue its merge")
	cmd.Flags().BoolVar(&acceptDrift, "accept-drift", false, "with --plan: accept deliberate input changes by recording the current input digest and resetting the plan to pending")
	cmd.Flags().BoolVar(&reset, "reset", false, "with --plan: discard the prior attempt — remove its worktree, delete its branch, and reset to a clean first-run")
	return cmd
}

func runPlanMarkCompleted(w io.Writer, root, planID string) error {
	rec, err := execution.MarkPlanCompleted(root, planID)
	if err != nil {
		return fmt.Errorf("mark plan %q completed: %w", planID, err)
	}

	fmt.Fprintf(w, "Marked plan %q completed: %s\n", planID, rec.Reason)
	fmt.Fprintln(w, "Run \"springfield start\" to perform the merge.")
	return nil
}

func runPlanAcceptDrift(w io.Writer, root, planID string) error {
	rec, err := execution.AcceptPlanDrift(root, planID, func(unit conductor.PlanUnit) (string, error) {
		return planrun.InputDigest(root, unit)
	})
	if err != nil {
		return fmt.Errorf("accept drift for plan %q: %w", planID, err)
	}

	fmt.Fprintf(w, "Accepted input drift for plan %q: %s\n", planID, rec.Reason)
	fmt.Fprintln(w, "Run \"springfield start\" to continue.")
	return nil
}

func runPlanReset(w io.Writer, root, planID string) error {
	rec, err := execution.ResetPlan(root, planID)
	if err != nil {
		return fmt.Errorf("reset plan %q: %w", planID, err)
	}

	fmt.Fprintf(w, "Reset plan %q: %s\n", planID, rec.Reason)
	fmt.Fprintln(w, "Removed its worktree and branch. Run \"springfield start\" to re-run from a clean base.")
	return nil
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

	// batch.json present does NOT prove the batch is alive: a crash (e.g. the
	// disk-full kill in dogfood #10) leaves batch.json behind with plans still
	// marked running and no owning process. Probe process liveness via the
	// control-plane flock to tell a genuinely-running batch from a process-dead
	// orphan, instead of keying off batch.json presence alone.
	// Only ENOENT routes to the archive path below; any other filesystem error
	// (permission, transient I/O) must fail closed so we never destroy live
	// state based on a degraded read.
	if _, statErr := os.Stat(paths.BatchPath()); statErr == nil {
		return reportActiveBatchLiveness(w, root, run.ActiveBatchID, diagnoseOnly)
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

// reportActiveBatchLiveness handles the batch.json-present case of orphan
// recovery. It probes whether a live springfield process owns the batch and
// either reports it as running, or (for a process-dead orphan) resets the
// stale running plan markers to interrupted so the next start can resume.
func reportActiveBatchLiveness(w io.Writer, root, batchID string, diagnoseOnly bool) error {
	live, err := execution.ResolveActiveBatchLiveness(root, !diagnoseOnly)
	if err != nil {
		return fmt.Errorf("inspect batch liveness: %w", err)
	}

	if live.Holder != nil {
		fmt.Fprintf(w, "Batch %q is running (pid %d since %s) — nothing to recover.\n", batchID, live.Holder.PID, live.Holder.Since.Format(time.RFC3339))
		fmt.Fprintln(w, "Wait for it to finish, or run \"springfield status\" to inspect.")
		return nil
	}

	if live.LockUnreadable {
		fmt.Fprintf(w, "WARNING: batch %q has a control-plane lock file that could not be read to confirm a holder (torn write or permission error). Proceeding as if no live process owns it — make sure no \"springfield start\" is actually running before relying on this.\n", batchID)
	}

	if len(live.StaleRunning) == 0 && len(live.Cleared) == 0 {
		fmt.Fprintf(w, "Batch %q has no live springfield process and no plans stuck running — nothing to recover.\n", batchID)
		fmt.Fprintln(w, "Run \"springfield start\" to resume or \"springfield status\" to inspect.")
		return nil
	}

	if diagnoseOnly {
		fmt.Fprintf(w, "Batch %q has no live springfield process, but %d plan(s) are still marked running (orphaned by a crash):\n", batchID, len(live.StaleRunning))
		for _, id := range live.StaleRunning {
			fmt.Fprintf(w, "  - %s\n", id)
		}
		fmt.Fprintln(w, "\nRun \"springfield recover\" to reset them to interrupted, then \"springfield start\" to resume.")
		return nil
	}

	fmt.Fprintf(w, "Batch %q had no live springfield process (crashed or killed). Reset %d orphaned running plan(s) to interrupted:\n", batchID, len(live.Cleared))
	for _, id := range live.Cleared {
		fmt.Fprintf(w, "  - %s\n", id)
	}
	fmt.Fprintln(w, "\nRun \"springfield start\" to resume.")
	return nil
}

func printOrphanDiagnosis(w io.Writer, root string, run batch.Run, paths batch.Paths) error {
	fmt.Fprintln(w, "Diagnosis:")
	fmt.Fprintf(w, "  run.json active_batch_id: %s\n", run.ActiveBatchID)
	if run.FatalError != "" {
		fmt.Fprintf(w, "  run.json fatal_error: %s\n", run.FatalError)
	}

	// Surface plan-level progress from conductor state. batch.json is gone in the
	// orphan path, so phase context is unavailable — but the registered plan
	// units + their integrated/running/pending breakdown is the most actionable
	// signal for someone deciding whether to archive or salvage. In a forensics
	// dump, an inability to read project state IS diagnostic signal: surface it
	// explicitly rather than silently omit the line.
	project, perr := conductor.LoadProjectRaw(root)
	switch {
	case perr != nil:
		fmt.Fprintf(w, "  plans registered: (unavailable — %v)\n", perr)
	default:
		total := len(project.Config.PlanUnits)
		var integrated, running int
		for _, unit := range project.Config.PlanUnits {
			switch conductor.ClassifyPlan(project.State.Plans[unit.ID]) {
			case conductor.BucketIntegrated:
				integrated++
			case conductor.BucketInFlight:
				running++
			}
		}
		fmt.Fprintf(w, "  plans registered: %d (integrated %d, running %d)\n", total, integrated, running)
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
