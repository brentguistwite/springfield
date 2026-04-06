package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

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
				line := fmt.Sprintf("  %s: %s", name, project.PlanStatus(name))
				if project.PlanStatus(name) == conductor.StatusFailed {
					line += fmt.Sprintf(" (%s)", project.PlanError(name))
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

			runner := conductor.NewRunner(project, &noopExecutor{})
			if err := runner.RunAll(); err != nil {
				return err
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), "All plans completed.")
			return err
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

			runner := conductor.NewRunner(project, &noopExecutor{})
			if err := runner.RunAll(); err != nil {
				return err
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), "All plans completed.")
			return err
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

type noopExecutor struct{}

func (e *noopExecutor) Execute(plan string) error {
	return nil
}
