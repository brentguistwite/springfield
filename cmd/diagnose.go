package cmd

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"

	"springfield/internal/features/workflow"
)

// NewDiagnoseCommand diagnoses Springfield work failures and suggests next steps.
func NewDiagnoseCommand() *cobra.Command {
	var dir string
	var workID string

	cmd := &cobra.Command{
		Use:   "diagnose",
		Short: "Diagnose Springfield work failures and suggest next steps.",
		Long:  "Diagnose Springfield work failures and suggest next steps.",
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

			diagnosis, err := runner.Diagnose(root, resolvedWorkID)
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Work: %s\n", diagnosis.WorkID)
			fmt.Fprintf(w, "Status: %s\n", diagnosis.Status)
			fmt.Fprintln(w, "")
			if len(diagnosis.Failures) == 0 {
				fmt.Fprintln(w, "No failures detected.")
			} else {
				fmt.Fprintln(w, "Failures:")
				for _, failure := range diagnosis.Failures {
					fmt.Fprintf(w, "  %s  %s\n", failure.Workstream, failure.Title)
					fmt.Fprintf(w, "    Error: %s\n", failure.Error)
					if failure.EvidencePath != "" {
						fmt.Fprintf(w, "    Evidence: %s\n", failure.EvidencePath)
					}
				}
			}
			fmt.Fprintln(w, "")
			fmt.Fprintf(w, "Next step: %s\n", diagnosis.NextStep)
			return nil
		},
	}

	bindWorkflowFlags(cmd, &dir, &workID)
	return cmd
}
