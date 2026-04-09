package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"springfield/internal/features/skills"
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

			rendered, err := skills.Render(root, "explain")
			if err != nil {
				return err
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), rendered.Prompt)
			return err
		},
	}
}
