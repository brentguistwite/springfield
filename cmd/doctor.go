package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewDoctorCommand exposes the future local diagnostics surface.
func NewDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check local Springfield setup.",
		Long:  "Doctor will check local Springfield setup. This placeholder keeps the diagnostics surface stable while bootstrap work lands.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "Doctor will check local Springfield setup. Placeholder only for now.")
			return err
		},
	}
}
