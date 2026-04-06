package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/config"
	"springfield/internal/core/runtime"
	"springfield/internal/features/ralph"
)

// NewRalphCommand exposes Ralph-specific subcommands.
func NewRalphCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ralph",
		Short: "Manage Ralph plans and runs from Springfield.",
		Long:  "Manage Ralph plans, story selection, and local run history.",
	}

	cmd.PersistentFlags().String("dir", ".", "Project root for Springfield runtime state")
	cmd.AddCommand(
		newRalphInitCommand(),
		newRalphStatusCommand(),
		newRalphRunCommand(),
	)

	return cmd
}

func ralphRootDir(cmd *cobra.Command) string {
	dir, _ := cmd.Flags().GetString("dir")
	return dir
}

func ralphWorkspace(cmd *cobra.Command) (ralph.Workspace, error) {
	return ralph.OpenRoot(ralphRootDir(cmd))
}

func newRalphInitCommand() *cobra.Command {
	var (
		name     string
		specPath string
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Persist a Ralph plan spec into project-local Springfield state.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" || specPath == "" {
				return fmt.Errorf("--name and --spec are required")
			}

			data, err := os.ReadFile(specPath)
			if err != nil {
				return fmt.Errorf("read Ralph spec: %w", err)
			}

			var spec ralph.Spec
			if err := json.Unmarshal(data, &spec); err != nil {
				return fmt.Errorf("decode Ralph spec: %w", err)
			}

			workspace, err := ralphWorkspace(cmd)
			if err != nil {
				return err
			}

			if err := workspace.InitPlan(name, spec); err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Initialized Ralph plan %q with %d stories.\n", name, len(spec.Stories))
			return err
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Plan name")
	cmd.Flags().StringVar(&specPath, "spec", "", "Path to a Ralph spec JSON file")

	return cmd
}

func newRalphStatusCommand() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show stored Ralph plan status.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			workspace, err := ralphWorkspace(cmd)
			if err != nil {
				return err
			}

			plan, err := workspace.LoadPlan(name)
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Plan: %s\n", plan.Name)
			fmt.Fprintf(w, "Project: %s\n", plan.Spec.Project)
			fmt.Fprintf(w, "Stories: %d\n\n", len(plan.Spec.Stories))

			for _, story := range plan.Spec.Stories {
				status := "pending"
				if story.Passed {
					status = "passed"
				}
				fmt.Fprintf(w, "%s  %s  %s\n", story.ID, status, story.Title)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Plan name")

	return cmd
}

func newRalphRunCommand() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the next eligible Ralph story.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			rootDir := ralphRootDir(cmd)

			cfg, err := config.LoadFrom(rootDir)
			if err != nil {
				return err
			}

			registry := agents.NewRegistry(
				claude.New(exec.LookPath),
				codex.New(exec.LookPath),
			)
			runner := runtime.NewRunner(registry)
			agentID := agents.ID(cfg.Config.Project.DefaultAgent)

			executor := ralph.NewRuntimeExecutor(runner, agentID, rootDir)

			workspace, err := ralphWorkspace(cmd)
			if err != nil {
				return err
			}

			record, err := workspace.RunNext(name, executor)
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Story %s: %s (agent: %s)\n", record.StoryID, record.Status, record.Agent)
			if record.Status == "failed" && record.Error != "" {
				fmt.Fprintf(w, "Error: %s\n", record.Error)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Plan name")

	return cmd
}
