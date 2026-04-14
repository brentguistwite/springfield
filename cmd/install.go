package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"springfield/internal/features/skills"
)

// NewInstallCommand wires the plugin installation entrypoint into the public CLI surface.
func NewInstallCommand() *cobra.Command {
	var hosts []string
	var claudeDir string
	var codexDir string

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install Springfield into Claude Code and Codex.",
		Long:  "Install Springfield into Claude Code and Codex. By default Springfield writes both host artifacts; use --host to limit installation to a specific target.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}

			installed, err := skills.Install(root, skills.InstallOptions{
				Hosts:     hosts,
				ClaudeDir: claudeDir,
				CodexDir:  codexDir,
			})
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			fmt.Fprintln(w, "Installed Springfield plugins:")
			for _, item := range installed {
				fmt.Fprintf(w, "  %s  %s\n", item.Host.Name, item.Path)
			}
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&hosts, "host", nil, "Install only selected targets: claude-code, codex")
	cmd.Flags().StringVar(&claudeDir, "claude-dir", "", "Override the Claude Code commands directory")
	cmd.Flags().StringVar(&codexDir, "codex-dir", "", "Override the Codex skills directory")
	return cmd
}
