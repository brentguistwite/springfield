package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewConductorCommand exposes the future Conductor-specific command surface.
func NewConductorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "conductor",
		Short: "Run Springfield conductor workflows.",
		Long:  "Conductor workflows will move behind Springfield. This placeholder keeps the command surface stable while the shared runtime is still bootstrapping.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "Conductor workflows will move behind Springfield. Placeholder only for now.")
			return err
		},
	}
}
