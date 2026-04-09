package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"springfield/internal/features/workflow"
)

// NewDiagnoseCommand diagnoses Springfield work failures and suggests next steps.
func NewDiagnoseCommand() *cobra.Command {
	var dir string
	var workID string

	cmd := &cobra.Command{
		Use:   "diagnose",
		Short: "Summarize Springfield failures, evidence, and next steps for the active work.",
		Long:  "Summarize Springfield failures, evidence, and next steps for the active work.",
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
			if diagnosis.Summary != "" {
				fmt.Fprintf(w, "Summary: %s\n", diagnosis.Summary)
			}
			if len(diagnosis.FailingWorkstreams) > 0 {
				fmt.Fprintf(w, "Failing workstreams: %s\n", strings.Join(diagnosis.FailingWorkstreams, ", "))
			}
			if diagnosis.LastError != "" {
				fmt.Fprintf(w, "Last error: %s\n", diagnosis.LastError)
			}
			if diagnosis.EvidencePath != "" {
				fmt.Fprintf(w, "Evidence: %s\n", diagnosis.EvidencePath)
			}
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
