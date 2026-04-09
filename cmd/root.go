package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"springfield/internal/features/tui"
)

// Version is injected at build time for release artifacts.
var Version = "dev"

// Execute runs the Springfield root command.
func Execute() error {
	return NewRootCommand().Execute()
}

// NewRootCommand builds the stable top-level CLI surface.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "springfield",
		Short:         "Springfield is the local-first product surface for planning and running work.",
		Long:          "Springfield is the local-first CLI and TUI entrypoint for defining, explaining, and running work.\n\nBare springfield opens the TUI-first Springfield shell.",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return tui.NewApp(os.Stdin, os.Stdout).Run()
		},
	}

	root.AddCommand(
		NewInitCommand(),
		NewExplainCommand(),
		NewSkillsCommand(),
		NewStatusCommand(),
		NewResumeCommand(),
		NewDiagnoseCommand(),
		NewTUICommand(),
		NewDoctorCommand(),
		NewVersionCommand(),
	)

	return root
}
