package cmd

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"

	"springfield/internal/features/workflow"
)

// NewResumeCommand runs or resumes approved Springfield work.
func NewResumeCommand() *cobra.Command {
	var dir string
	var workID string

	cmd := &cobra.Command{
		Use:   "resume",
		Short: "Run or resume approved Springfield work.",
		Long:  "Run or resume approved Springfield work.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, resolvedWorkID, err := resolveWorkflowTarget(dir, workID)
			if err != nil {
				return err
			}

			runner, err := workflow.NewRuntimeRunner(root, exec.LookPath, nil)
			if err != nil {
				return err
			}

			result, err := runner.Resume(root, resolvedWorkID)
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Work %s: %s\n", result.WorkID, result.Status)
			if result.Error != "" {
				fmt.Fprintf(w, "Error: %s\n", result.Error)
			}
			return err
		},
	}

	bindWorkflowFlags(cmd, &dir, &workID)
	return cmd
}
