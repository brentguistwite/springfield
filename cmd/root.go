package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const rootUsageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Available Commands:{{range $cmds}}{{if .IsAvailableCommand}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) .IsAvailableCommand)}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Additional Commands:{{range $cmds}}{{if (and (eq .GroupID "") .IsAvailableCommand)}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`

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
		Short:         "Springfield is plugin-first for agent-native work.",
		Long:          "Springfield is plugin-first for agent-native work.\n\nPrimary install path: use the Claude marketplace or Codex plugin/catalog entry. Use init to scaffold project state, install to sync local Claude Code and Codex artifacts for bootstrap or fallback workflows, doctor to verify local tooling, and status or resume to manage approved work.",
		SilenceErrors: true,
		SilenceUsage:  true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Help(); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "\nPrimary install path: use the Claude marketplace or Codex plugin/catalog entry.\nFor local host sync or fallback: run \"springfield install\".")
			return err
		},
	}
	root.SetUsageTemplate(rootUsageTemplate)

	root.AddCommand(
		NewInitCommand(),
		NewInstallCommand(),
		NewStatusCommand(),
		NewResumeCommand(),
		NewDoctorCommand(),
		NewVersionCommand(),
	)

	root.SetHelpCommand(&cobra.Command{
		Use:    "help [command]",
		Short:  "Help about any command",
		Long:   "Help about any command",
		Hidden: true,
		Args:   cobra.ArbitraryArgs,
		Run: func(cmd *cobra.Command, args []string) {
			root.HelpFunc()(root, args)
		},
	})

	return root
}
