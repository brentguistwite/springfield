package cmd

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"os/exec"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
	"springfield/internal/core/config"
	"springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
	"springfield/internal/features/execution"
)

// NewConductorCommand exposes the conductor command surface.
func NewConductorCommand() *cobra.Command {
	root := &cobra.Command{
		Use:    "conductor",
		Short:  "Run Springfield conductor workflows.",
		Long:   "Orchestrate plan execution, check status, resume from failures, and diagnose issues.",
		Hidden: true,
	}

	root.AddCommand(
		newConductorSetupCommand(),
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

func buildConductorExecutor(project *conductor.Project, dir string) (*conductor.RuntimeExecutor, error) {
	loaded, err := config.LoadFrom(dir)
	if err != nil {
		return nil, err
	}

	registry := agents.NewRegistry(
		claude.New(exec.LookPath),
		codex.New(exec.LookPath),
		gemini.New(exec.LookPath),
	)
	runner := runtime.NewRunner(registry)

	priority := make([]agents.ID, 0, len(loaded.Config.EffectivePriority()))
	for _, id := range loaded.Config.EffectivePriority() {
		if id == "" {
			continue
		}
		priority = append(priority, agents.ID(id))
	}

	plansDir := project.Config.PlansDir
	if !filepath.IsAbs(plansDir) {
		plansDir = filepath.Join(loaded.RootDir, plansDir)
	}

	return conductor.NewRuntimeExecutor(runner, priority, plansDir, loaded.RootDir, loaded.Config.ExecutionSettings()), nil
}

func newConductorSetupCommand() *cobra.Command {
	var dir string
	var tool string

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Generate conductor config from guided defaults.",
		Long:  "Create .springfield/conductor/config.json so the conductor is ready to run without manual JSON editing.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}

			priority := loaded.Config.EffectivePriority()
			effectiveTool := tool
			if effectiveTool == "" && len(priority) > 0 {
				effectiveTool = priority[0]
			}
			fallbackTool := ""
			for _, candidate := range priority {
				if candidate == "" || candidate == effectiveTool {
					continue
				}
				fallbackTool = candidate
				break
			}

			input := execution.Defaults()
			priorityForSetup := effectivePriority(effectiveTool, fallbackTool)

			ready, err := execution.IsReady(loaded.RootDir)
			if err != nil {
				return err
			}
			if !ready {
				plansDir, err := promptConductorSetup(cmd)
				if err != nil {
					return err
				}
				input.PlansDir = plansDir
			}

			result, err := execution.Setup(loaded.RootDir, priorityForSetup, input)
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			if result.Created {
				fmt.Fprintf(w, "Created %s\n", result.Path)
				fmt.Fprintln(w, "")
				fmt.Fprintf(w, "Next steps:\n")
				fmt.Fprintf(w, "  1. Add plan files to %s\n", input.PlansDir)
				fmt.Fprintf(w, "  2. Run: springfield internal-debug conductor run\n")

				// Agent prerequisite guidance
				fmt.Fprintln(w, "")
				fmt.Fprintln(w, "Agent prerequisites:")
				printAgentGuidance(w, effectiveTool)
			} else {
				fmt.Fprintf(w, "Execution config already exists at %s, reusing.\n", result.Path)
			}

			return nil
		},
	}
	bindDirFlag(cmd, &dir)
	cmd.Flags().StringVar(&tool, "tool", "", "agent tool to use (default: from springfield.toml)")
	return cmd
}

func promptConductorSetup(cmd *cobra.Command) (string, error) {
	reader := bufio.NewReader(cmd.InOrStdin())
	w := cmd.OutOrStdout()

	fmt.Fprintln(w, "Plan storage mode:")
	fmt.Fprintf(w, "  %s  %s\n", "local", execution.LocalPlansDir)
	fmt.Fprintf(w, "  %s  %s\n", "tracked", execution.TrackedPlansDir)
	fmt.Fprint(w, "Choose plan storage [local/tracked] (default: local): ")

	choice, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	choice = strings.ToLower(strings.TrimSpace(choice))

	switch choice {
	case "", "local", "l":
		return execution.LocalPlansDir, nil
	case "tracked", "t":
		return execution.TrackedPlansDir, nil
	default:
		return "", fmt.Errorf("invalid plan storage choice %q", choice)
	}
}

func effectivePriority(primary, fallback string) []string {
	priority := make([]string, 0, 2)
	if primary != "" {
		priority = append(priority, primary)
	}
	if fallback != "" && fallback != primary {
		priority = append(priority, fallback)
	}
	return priority
}

func printAgentGuidance(w io.Writer, tool string) {
	switch tool {
	case "claude":
		fmt.Fprintln(w, "  Claude Code CLI must be installed and authenticated.")
		fmt.Fprintln(w, "  Install: npm install -g @anthropic-ai/claude-code")
		fmt.Fprintln(w, "  Auth:    claude /login")
	case "codex":
		fmt.Fprintln(w, "  Codex CLI must be installed and authenticated.")
		fmt.Fprintln(w, "  Install: npm install -g @openai/codex")
		fmt.Fprintln(w, "  Auth:    Set OPENAI_API_KEY in your environment")
	default:
		fmt.Fprintf(w, "  Ensure %q CLI is installed and authenticated.\n", tool)
	}
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

			executor, err := buildConductorExecutor(project, dir)
			if err != nil {
				return err
			}
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

			executor, err := buildConductorExecutor(project, dir)
			if err != nil {
				return err
			}
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

	if diagnosis.Total == 0 {
		fmt.Fprintln(w, "No plans configured. Add plans to your conductor config, then run again.")
		return nil
	}

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
	if len(project.AllPlans()) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No plans configured. Add plans to your conductor config, then run again.")
		return err
	}

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
