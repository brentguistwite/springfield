package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"springfield/internal/features/tui"
)

// NewTUICommand opens the Springfield TUI shell explicitly.
func NewTUICommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the Springfield TUI shell.",
		Long:  "Open the temporary Springfield TUI placeholder. Bare springfield opens the TUI-first Springfield shell.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return tui.NewApp(os.Stdin, os.Stdout).Run()
		},
	}
}
