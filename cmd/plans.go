package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/execution"
)

// NewPlansCommand groups Springfield-managed plan-registry commands.
func NewPlansCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plans",
		Short: "Manage Springfield plan-unit registry.",
		Long:  "Manage the Springfield plan-unit registry: register, list, reorder, or remove configured plans.",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newPlansAddCommand(),
		newPlansListCommand(),
		newPlansRemoveCommand(),
		newPlansReorderCommand(),
	)
	return cmd
}

func newPlansAddCommand() *cobra.Command {
	var (
		dir         string
		id          string
		title       string
		description string
		path        string
		ref         string
		planBranch  string
		order       int
	)

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Register a new plan unit.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id is required")
			}
			if path == "" {
				return fmt.Errorf("--path is required")
			}
			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}
			plan, err := execution.AddPlan(loaded.RootDir, execution.PlanInput{
				ID:          id,
				Title:       title,
				Description: description,
				Path:        path,
				Ref:         ref,
				PlanBranch:  planBranch,
				Order:       order,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Registered plan %q (order %d) at %s\n", plan.ID, plan.Order, plan.Path)
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	cmd.Flags().StringVar(&id, "id", "", "stable plan id (slug)")
	cmd.Flags().StringVar(&title, "title", "", "human-friendly plan title")
	cmd.Flags().StringVar(&description, "description", "", "optional plan description")
	cmd.Flags().StringVar(&path, "path", "", "plan source path (project-relative or plans_dir-relative filename)")
	cmd.Flags().StringVar(&ref, "ref", "", "optional base ref the plan branches from")
	cmd.Flags().StringVar(&planBranch, "plan-branch", "", "optional explicit branch name for the plan worktree")
	cmd.Flags().IntVar(&order, "order", 0, "1-based execution order; defaults to next available slot")
	return cmd
}

func newPlansListCommand() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured plan units in execution order.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}
			plans, err := execution.ListPlans(loaded.RootDir)
			if err != nil {
				return err
			}
			renderPlanList(cmd.OutOrStdout(), plans)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	return cmd
}

func newPlansRemoveCommand() *cobra.Command {
	var (
		dir string
		id  string
	)
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove a plan unit by id.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id is required")
			}
			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}
			if err := execution.RemovePlan(loaded.RootDir, id); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed plan %q.\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	cmd.Flags().StringVar(&id, "id", "", "plan id to remove")
	return cmd
}

func newPlansReorderCommand() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "reorder <id> [<id>...]",
		Short: "Reorder plan units by listing every id in the new execution order.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}
			if err := execution.ReorderPlans(loaded.RootDir, args); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Reordered plan units.")
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	return cmd
}

func renderPlanList(w io.Writer, plans []execution.Plan) {
	if len(plans) == 0 {
		fmt.Fprintln(w, "No plan units configured. Run \"springfield plans add\" to register one.")
		return
	}
	for i, p := range plans {
		title := p.Title
		if title == "" {
			title = p.ID
		}
		fmt.Fprintf(w, "%d. %s — %s\n", i+1, p.ID, title)
		fmt.Fprintf(w, "   path: %s\n", p.Path)
		if p.Ref != "" {
			fmt.Fprintf(w, "   ref: %s\n", p.Ref)
		}
		if p.PlanBranch != "" {
			fmt.Fprintf(w, "   branch: %s\n", p.PlanBranch)
		}
	}
}
