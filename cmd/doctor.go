package cmd

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/catalog"
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
			registry := agents.NewRegistry(catalog.DefaultAdapters(exec.LookPath)...)

			report := doctor.Run(cmd.Context(), registry)
			w := cmd.OutOrStdout()

			for _, check := range report.Checks {
				icon := "✓"
				switch check.Status {
				case doctor.StatusMissing:
					icon = "✗"
				case doctor.StatusUnhealthy:
					icon = "!"
				}

				_, _ = fmt.Fprintf(w, "  %s %s (%s)", icon, check.Name, check.Binary)
				if check.Path != "" {
					_, _ = fmt.Fprintf(w, " → %s", check.Path)
				}
				_, _ = fmt.Fprintln(w)

				if check.Guidance != "" {
					_, _ = fmt.Fprintf(w, "    %s\n", check.Guidance)
				}
			}

			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, report.Summary)

			return nil
		},
	}
}
