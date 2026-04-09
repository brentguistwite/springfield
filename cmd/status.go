package cmd

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/workflow"
)

// NewStatusCommand shows Springfield work status from approved work state.
func NewStatusCommand() *cobra.Command {
	var dir string
	var workID string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Springfield work status from approved work state.",
		Long:  "Show Springfield work status from approved work state.",
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

			status, err := runner.Status(root, resolvedWorkID)
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Work: %s\n", status.WorkID)
			fmt.Fprintf(w, "Title: %s\n", status.Title)
			fmt.Fprintf(w, "Split: %s\n", status.Split)
			fmt.Fprintf(w, "Status: %s\n", status.Status)
			fmt.Fprintln(w, "")
			fmt.Fprintln(w, "Workstreams:")
			for _, workstream := range status.Workstreams {
				fmt.Fprintf(w, "  %s  %s  %s\n", workstream.Name, workstream.Status, workstream.Title)
				if workstream.Error != "" {
					fmt.Fprintf(w, "    Error: %s\n", workstream.Error)
				}
				if workstream.EvidencePath != "" {
					fmt.Fprintf(w, "    Evidence: %s\n", workstream.EvidencePath)
				}
			}
			return nil
		},
	}

	bindWorkflowFlags(cmd, &dir, &workID)
	return cmd
}

func bindWorkflowFlags(cmd *cobra.Command, dir *string, workID *string) {
	cmd.Flags().StringVar(dir, "dir", ".", "project root or nested path inside the Springfield project")
	cmd.Flags().StringVar(workID, "work", "", "Springfield work id (default: current work)")
}

func resolveWorkflowTarget(dir, workID string) (string, string, error) {
	loaded, err := config.LoadFrom(dir)
	if err != nil {
		return "", "", err
	}

	resolvedWorkID := workID
	if resolvedWorkID == "" {
		resolvedWorkID, err = workflow.CurrentWorkID(loaded.RootDir)
		if err != nil {
			return "", "", err
		}
	}

	return loaded.RootDir, resolvedWorkID, nil
}
