package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"springfield/internal/core/agents"
	"springfield/internal/core/config"
)

// isTTY reports whether fd is an interactive terminal.
func isTTY(fd int) bool {
	return term.IsTerminal(fd)
}

// NewInitCommand creates the `springfield init` subcommand.
func NewInitCommand() *cobra.Command {
	var agentsFlag string
	var resetFlag bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new Springfield project in the current directory.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}

			interactive := isTTY(int(os.Stdin.Fd()))
			priority, err := resolvePriority(agentsFlag, interactive, cmd.InOrStdin(), cmd.OutOrStdout())
			if err != nil {
				return err
			}

			result, err := config.Init(dir, priority, config.InitOptions{Reset: resetFlag})
			if err != nil {
				return err
			}

			if result.BackupPath != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Backed up previous %s to %s\n", config.FileName, result.BackupPath)
			}

			switch {
			case result.ConfigCreated || result.BackupPath != "":
				fmt.Fprintln(cmd.OutOrStdout(), "Created "+config.FileName)
			case result.ConfigUpdated:
				fmt.Fprintln(cmd.OutOrStdout(), "Updated "+config.FileName+" with recommended defaults")
			default:
				fmt.Fprintln(cmd.OutOrStdout(), config.FileName+" already up to date")
			}

			if result.RuntimeDirCreated {
				fmt.Fprintln(cmd.OutOrStdout(), "Created .springfield/")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), ".springfield/ already exists, skipping")
			}

			fmt.Fprintln(cmd.OutOrStdout())
			fmt.Fprintln(cmd.OutOrStdout(), "Next: install Springfield from the Claude marketplace or Codex plugin/catalog. Use \"springfield install\" only for local host sync, bootstrap, or fallback workflows.")

			return nil
		},
	}

	cmd.Flags().StringVar(&agentsFlag, "agents", "", "Comma-separated agent priority list (e.g. claude,codex)")
	cmd.Flags().BoolVar(&resetFlag, "reset", false, "Back up existing config and rewrite from scratch (destructive)")

	return cmd
}

// resolvePriority determines the agent priority list from flag, prompt, or default.
// interactive=true prompts the user via in/out; false returns the default list.
func resolvePriority(agentsFlag string, interactive bool, in io.Reader, out io.Writer) ([]string, error) {
	if agentsFlag != "" {
		return parseAndValidateAgents(agentsFlag)
	}

	if !interactive {
		return defaultPriority(), nil
	}

	return promptForAgents(in, out)
}

// promptForAgents prompts the user interactively. Allows 4 attempts total.
func promptForAgents(in io.Reader, out io.Writer) ([]string, error) {
	defaults := defaultPriority()
	defaultStr := strings.Join(defaults, ",")

	buf := new(strings.Builder)
	scratch := make([]byte, 1)

	for attempt := 0; attempt < 4; attempt++ {
		fmt.Fprintf(out, "Enter agents in priority order (comma-separated) [%s]: ", defaultStr)

		buf.Reset()
		for {
			n, err := in.Read(scratch)
			if n > 0 {
				ch := scratch[0]
				if ch == '\n' {
					break
				}
				buf.WriteByte(ch)
			}
			if err != nil {
				if err == io.EOF {
					break
				}
				return nil, fmt.Errorf("read input: %w", err)
			}
		}

		line := strings.TrimSpace(buf.String())
		if line == "" {
			return defaults, nil
		}

		priority, err := parseAndValidateAgents(line)
		if err != nil {
			fmt.Fprintf(out, "Error: %v\n", err)
			continue
		}
		return priority, nil
	}

	return nil, fmt.Errorf("too many invalid attempts; aborting")
}

// parseAndValidateAgents splits a comma-separated agent string and validates each entry.
func parseAndValidateAgents(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	priority := make([]string, 0, len(parts))
	for _, p := range parts {
		id := strings.TrimSpace(p)
		if id == "" {
			continue
		}
		if !agents.IsExecutionSupported(agents.ID(id)) {
			return nil, fmt.Errorf("%s is not yet supported for execution", id)
		}
		priority = append(priority, id)
	}
	if len(priority) == 0 {
		return nil, fmt.Errorf("at least one agent is required")
	}
	return priority, nil
}

// defaultPriority returns the canonical execution-supported agent list as strings.
func defaultPriority() []string {
	ids := agents.SupportedForExecution()
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}
