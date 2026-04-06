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
		Long:  "Open the Springfield TUI shell. Running bare `springfield` also opens this shell.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return tui.NewApp(os.Stdin, os.Stdout).Run()
		},
	}
}
