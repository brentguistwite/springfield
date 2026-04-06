package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewVersionCommand prints the Springfield build version.
func NewVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Springfield version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "springfield %s\n", Version)
			return err
		},
	}
}
