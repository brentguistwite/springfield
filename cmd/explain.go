package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"springfield/internal/features/playbooks"
)

// NewExplainCommand renders the Springfield explanation prompt for the current project.
func NewExplainCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "explain",
		Short: "Render the built-in Springfield explanation prompt for the current project.",
		Long:  "Render the built-in Springfield explanation prompt for the current project.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}

			output, err := playbooks.Build(playbooks.Input{
				Kind:        playbooks.KindConductor,
				ProjectRoot: root,
				TaskBody: strings.TrimSpace(`
Explain how Springfield should approach work in this project.

Keep Springfield as the only user-facing surface.
Explain the current project context and the built-in playbook guidance that will shape planning and execution.
`),
			})
			if err != nil {
				return err
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), output.Prompt)
			return err
		},
	}
}
