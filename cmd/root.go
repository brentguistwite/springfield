package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"springfield/internal/features/tui"
)

// Execute runs the Springfield root command.
func Execute() error {
	return NewRootCommand().Execute()
}

// NewRootCommand builds the stable top-level CLI surface.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "springfield",
		Short:         "Springfield unifies Ralph and Ralph Conductor behind one local-first surface.",
		Long:          "Springfield is the local-first CLI and TUI entrypoint for the unified Ralph product surface.\n\nBare springfield opens the TUI-first Springfield shell.",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return tui.NewApp(os.Stdin, os.Stdout).Run()
		},
	}

	root.AddCommand(
		NewInitCommand(),
		NewTUICommand(),
		NewRalphCommand(),
		NewConductorCommand(),
		NewDoctorCommand(),
	)

	return root
}
