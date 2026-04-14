package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
)

// NewInitCommand creates the `springfield init` subcommand.
func NewInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a new Springfield project in the current directory.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}

			result, err := config.Init(dir)
			if err != nil {
				return err
			}

			if result.ConfigCreated {
				fmt.Fprintln(cmd.OutOrStdout(), "Created "+config.FileName)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), config.FileName+" already exists, skipping")
			}

			if result.RuntimeDirCreated {
				fmt.Fprintln(cmd.OutOrStdout(), "Created .springfield/")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), ".springfield/ already exists, skipping")
			}

			fmt.Fprintln(cmd.OutOrStdout(), "")
			fmt.Fprintln(cmd.OutOrStdout(), "Next: run \"springfield install\" to set up Springfield for your agent hosts.")

			return nil
		},
	}
}
