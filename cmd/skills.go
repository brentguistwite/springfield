package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"springfield/internal/features/skills"
)

// NewSkillsCommand exposes Springfield-owned direct skill wrappers for power users.
func NewSkillsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Install or inspect optional Springfield direct skill wrappers.",
		Long:  "Install or inspect optional Springfield direct skill wrappers. These are optional power-user wrappers over Springfield playbooks, not the primary product flow.",
	}

	cmd.AddCommand(
		newSkillsListCommand(),
		newSkillsInstallCommand(),
	)

	return cmd
}

func newSkillsListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the optional Springfield direct skills.",
		Long:  "List the optional Springfield direct skills.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			fmt.Fprintln(w, "Available Springfield direct skills:")
			for _, skill := range skills.Catalog() {
				fmt.Fprintf(w, "  %s  %s\n", skill.Name, skill.Summary)
			}
			return nil
		},
	}
}

func newSkillsInstallCommand() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "install [skill...]",
		Short: "Install selected Springfield direct skills into a target directory.",
		Long:  "Install selected Springfield direct skills into a target directory.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}

			installed, err := skills.Install(root, dir, args)
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			fmt.Fprintln(w, "Installed Springfield skill wrappers:")
			for _, item := range installed {
				fmt.Fprintf(w, "  %s  %s\n", item.Skill.Name, item.Path)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "Directory that should receive Springfield skill wrapper folders")
	return cmd
}
