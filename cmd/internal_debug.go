package cmd

import "github.com/spf13/cobra"

func newInternalDebugCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "internal-debug",
		Short:  "Internal migration and debug commands.",
		Long:   "Internal migration and debug commands for Springfield engine maintenance.",
		Hidden: true,
	}

	ralph := NewRalphCommand()
	ralph.Hidden = false

	conductor := NewConductorCommand()
	conductor.Hidden = false

	cmd.AddCommand(ralph, conductor)
	return cmd
}
