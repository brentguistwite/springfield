package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewRalphCommand exposes the future Ralph-specific command surface.
func NewRalphCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ralph",
		Short: "Run Ralph workflows from the Springfield surface.",
		Long:  "Ralph workflows will move behind Springfield. This placeholder keeps the public command surface stable while the unified runtime lands.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "Ralph workflows will move behind Springfield. Placeholder only for now.")
			return err
		},
	}
}
