package cmd

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
	"springfield/internal/features/doctor"
)

// NewDoctorCommand wires the doctor feature into the CLI surface.
func NewDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check local Springfield setup.",
		Long:  "Doctor checks that supported agent CLIs are installed and reachable, providing install guidance for anything missing.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := agents.NewRegistry(
				claude.New(exec.LookPath),
				codex.New(exec.LookPath),
				gemini.New(exec.LookPath),
			)

			report := doctor.Run(cmd.Context(), registry)
			w := cmd.OutOrStdout()

			for _, check := range report.Checks {
				icon := "✓"
				if check.Status == doctor.StatusMissing {
					icon = "✗"
				} else if check.Status == doctor.StatusUnhealthy {
					icon = "!"
				}

				fmt.Fprintf(w, "  %s %s (%s)", icon, check.Name, check.Binary)
				if check.Path != "" {
					fmt.Fprintf(w, " → %s", check.Path)
				}
				fmt.Fprintln(w)

				if check.Guidance != "" {
					fmt.Fprintf(w, "    %s\n", check.Guidance)
				}
			}

			fmt.Fprintln(w)
			fmt.Fprintln(w, report.Summary)

			return nil
		},
	}
}
