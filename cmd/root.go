package cmd

import "github.com/spf13/cobra"

// Execute runs the Springfield root command.
func Execute() error {
	return NewRootCommand().Execute()
}

// NewRootCommand builds the stable top-level CLI surface.
func NewRootCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "springfield",
		Short:         "Springfield unifies Ralph and Ralph Conductor behind one local-first surface.",
		Long:          "Springfield is the local-first CLI and TUI entrypoint for the unified Ralph product surface.",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
}
