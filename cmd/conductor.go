package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"os/exec"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
)

// NewConductorCommand exposes the conductor command surface.
func NewConductorCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "conductor",
		Short: "Run Springfield conductor workflows.",
		Long:  "Orchestrate plan execution, check status, resume from failures, and diagnose issues.",
	}

	root.AddCommand(
		newConductorStatusCommand(),
		newConductorRunCommand(),
		newConductorResumeCommand(),
		newConductorDiagnoseCommand(),
	)

	return root
}

func bindDirFlag(cmd *cobra.Command, dir *string) {
	cmd.Flags().StringVar(dir, "dir", ".", "project root or nested path inside the Springfield project")
}

func buildConductorExecutor(project *conductor.Project, dir string) *conductor.RuntimeExecutor {
	registry := agents.NewRegistry(
		claude.New(exec.LookPath),
		codex.New(exec.LookPath),
	)
	runner := runtime.NewRunner(registry)
	agentID := agents.ID(project.Config.Tool)
	plansDir := filepath.Join(dir, project.Config.PlansDir)
	return conductor.NewRuntimeExecutor(runner, agentID, plansDir, dir)
}

func newConductorStatusCommand() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show conductor execution status.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := conductor.LoadProject(dir)
			if err != nil {
				return err
			}

			diagnosis := conductor.Diagnose(project)
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Progress: %d/%d plans completed\n", diagnosis.Completed, diagnosis.Total); err != nil {
				return err
			}
			for _, name := range project.AllPlans() {
				status := project.PlanStatus(name)
				line := fmt.Sprintf("  %s: %s", name, status)
				agent := project.PlanAgent(name)
				if agent != "" {
					line += fmt.Sprintf(" [%s]", agent)
				}
				if status == conductor.StatusFailed {
					line += fmt.Sprintf(" (%s)", project.PlanError(name))
					if ev := project.PlanEvidencePath(name); ev != "" {
						line += fmt.Sprintf("\n    evidence: %s", ev)
					}
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), line); err != nil {
					return err
				}
			}

			return nil
		},
	}
	bindDirFlag(cmd, &dir)
	return cmd
}

func newConductorRunCommand() *cobra.Command {
	var dir string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute all conductor plans from the beginning.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := conductor.LoadProject(dir)
			if err != nil {
				return err
			}

			project.ResetState()
			if dryRun {
				return printConductorDryRun(cmd, project)
			}

			executor := buildConductorExecutor(project, dir)
			runner := conductor.NewRunner(project, executor)
			runErr := runner.RunAll()

			return printConductorResult(cmd, project, runErr)
		},
	}
	bindDirFlag(cmd, &dir)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the first phase without running it")
	return cmd
}

func newConductorResumeCommand() *cobra.Command {
	var dir string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "resume",
		Short: "Resume conductor from the next incomplete phase.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := conductor.LoadProject(dir)
			if err != nil {
				return err
			}

			if dryRun {
				return printConductorDryRun(cmd, project)
			}

			executor := buildConductorExecutor(project, dir)
			runner := conductor.NewRunner(project, executor)
			runErr := runner.RunAll()

			return printConductorResult(cmd, project, runErr)
		},
	}
	bindDirFlag(cmd, &dir)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the next phase without running it")
	return cmd
}

func newConductorDiagnoseCommand() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "diagnose",
		Short: "Diagnose conductor failures and suggest next steps.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := conductor.LoadProject(dir)
			if err != nil {
				return err
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), conductor.Diagnose(project).Report())
			return err
		},
	}
	bindDirFlag(cmd, &dir)
	return cmd
}

func printConductorResult(cmd *cobra.Command, project *conductor.Project, runErr error) error {
	diagnosis := conductor.Diagnose(project)
	w := cmd.OutOrStdout()

	if diagnosis.Done {
		_, err := fmt.Fprintf(w, "Completed %d/%d plans successfully.\n", diagnosis.Completed, diagnosis.Total)
		return err
	}

	if len(diagnosis.Failures) > 0 {
		fmt.Fprintf(w, "Stopped: %d/%d plans completed, %d failed.\n", diagnosis.Completed, diagnosis.Total, len(diagnosis.Failures))
		for _, f := range diagnosis.Failures {
			fmt.Fprintf(w, "  - %s: %s\n", f.Plan, f.Error)
			if f.EvidencePath != "" {
				fmt.Fprintf(w, "    evidence: %s\n", f.EvidencePath)
			}
		}
	}

	if runErr != nil {
		return runErr
	}
	return nil
}

func printConductorDryRun(cmd *cobra.Command, project *conductor.Project) error {
	schedule := conductor.BuildSchedule(project.Config)
	next := schedule.NextPlans(project.State)
	if len(next) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "All plans already complete.")
		return err
	}

	if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Next plans to execute:"); err != nil {
		return err
	}
	for _, name := range next {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", name); err != nil {
			return err
		}
	}

	completed, total := schedule.Progress(project.State)
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Progress: %d/%d completed\n", completed, total)
	return err
}
