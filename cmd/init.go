package cmd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/catalog"
	"springfield/internal/core/config"
)

// maxPromptAttempts bounds retries on invalid interactive input so a misconfigured
// TTY or pasted garbage can't trap the caller in an infinite re-prompt loop.
const maxPromptAttempts = 4

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

			if added, err := ensureGitignoreEntry(dir, ".springfield/"); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to update .gitignore: %v\n", err)
			} else if added {
				fmt.Fprintln(cmd.OutOrStdout(), "Added .springfield/ to .gitignore")
			}

			for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
				added, err := ensureGuardrailBlock(filepath.Join(dir, name))
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to update %s: %v\n", name, err)
					continue
				}
				if added {
					fmt.Fprintf(cmd.OutOrStdout(), "Added Springfield guardrail to %s\n", name)
				}
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

// resolvePriority determines the agent priority list from flag or interactive
// prompt. Non-interactive callers must pass --agents explicitly — there is no
// fixed default priority for fresh init.
func resolvePriority(agentsFlag string, interactive bool, in io.Reader, out io.Writer) ([]string, error) {
	if agentsFlag != "" {
		return parseAndValidateAgents(agentsFlag)
	}

	if !interactive {
		return nil, fmt.Errorf(
			"non-interactive init requires --agents flag (e.g. --agents claude,codex,gemini)")
	}

	return promptForAgents(in, out)
}

// Detector reports detection status for execution-supported agents. Exported
// so external test packages (tests/cmd) can supply fakes without touching
// internals.
type Detector interface {
	Detect(id agents.ID) agents.DetectionStatus
}

// PromptForAgentsWithDetection runs the multi-agent picker. It surfaces each
// execution-supported agent alongside its current detection state so users
// can see at a glance which CLIs are installed before opting in. The user
// supplies a comma-separated priority list of the agents they want enabled;
// empty input is rejected and re-prompts up to maxPromptAttempts.
//
// The bufio.Reader is constructed once outside the retry loop so its internal
// buffer is shared across attempts — constructing it per-attempt would strand
// any bytes already read past the current line.
func PromptForAgentsWithDetection(in io.Reader, out io.Writer, det Detector) ([]string, error) {
	fmt.Fprintln(out, "Which agents do you want Springfield to use? (order = priority)")
	fmt.Fprintln(out)
	for _, id := range agents.SupportedForExecution() {
		marker := "✗ not found"
		switch det.Detect(id) {
		case agents.DetectionStatusAvailable:
			marker = "✓ detected on PATH"
		case agents.DetectionStatusUnhealthy:
			marker = "⚠ found but unhealthy"
		}
		fmt.Fprintf(out, "  %s — %s\n", id, marker)
	}
	fmt.Fprintln(out)

	reader := bufio.NewReader(in)
	for attempt := 0; attempt < maxPromptAttempts; attempt++ {
		fmt.Fprint(out, "Enter agents in priority order (comma-separated, e.g. claude,codex): ")

		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("read input: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			fmt.Fprintln(out, "Error: at least one agent is required")
			if errors.Is(err, io.EOF) {
				// Stream exhausted; retrying would reprompt with no input to read.
				break
			}
			continue
		}

		priority, parseErr := parseAndValidateAgents(line)
		if parseErr != nil {
			fmt.Fprintf(out, "Error: %v\n", parseErr)
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}
		return priority, nil
	}

	return nil, fmt.Errorf("too many invalid attempts; aborting")
}

// promptForAgents runs the interactive picker against real adapter detection.
// Internal wrapper around PromptForAgentsWithDetection — it constructs a
// production registryDetector backed by os/exec.LookPath so call sites in
// resolvePriority don't need to know about the Detector seam.
func promptForAgents(in io.Reader, out io.Writer) ([]string, error) {
	return PromptForAgentsWithDetection(in, out, newRegistryDetector(exec.LookPath))
}

// registryDetector is the production Detector implementation. It runs a real
// adapter detection sweep once at construction time and indexes the results
// by agent ID so the picker can look them up cheaply per-row.
type registryDetector struct {
	statuses map[agents.ID]agents.DetectionStatus
}

func newRegistryDetector(lookPath agents.LookPathFunc) registryDetector {
	registry := agents.NewRegistry(catalog.DefaultAdapters(lookPath)...)
	detections := registry.DetectAll(context.Background())
	statuses := make(map[agents.ID]agents.DetectionStatus, len(detections))
	for _, d := range detections {
		statuses[d.ID] = d.Status
	}
	return registryDetector{statuses: statuses}
}

func (r registryDetector) Detect(id agents.ID) agents.DetectionStatus {
	if s, ok := r.statuses[id]; ok {
		return s
	}
	return agents.DetectionStatusMissing
}

// parseAndValidateAgents splits a comma-separated agent string and validates each entry.
// Duplicate agent IDs are rejected because agent_priority must be a strict ordering.
func parseAndValidateAgents(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	priority := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		id := strings.TrimSpace(p)
		if id == "" {
			continue
		}
		if !agents.IsExecutionSupported(agents.ID(id)) {
			return nil, fmt.Errorf("%s is not yet supported for execution", id)
		}
		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("duplicate agent %q in priority list", id)
		}
		seen[id] = struct{}{}
		priority = append(priority, id)
	}
	if len(priority) == 0 {
		return nil, fmt.Errorf("at least one agent is required")
	}
	return priority, nil
}

// gitignoreComment explains the Springfield entry to anyone browsing .gitignore.
const gitignoreComment = "# Springfield runtime state (batches, run.json, logs, archive) — local only; safe to delete."

// ensureGitignoreEntry adds entry to <dir>/.gitignore if not already listed.
// Creates the file when missing. Idempotent across path-variant spellings
// (.springfield, .springfield/, /.springfield, /.springfield/).
func ensureGitignoreEntry(dir, entry string) (added bool, err error) {
	path := filepath.Join(dir, ".gitignore")

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("read .gitignore: %w", err)
	}

	if containsGitignoreEntry(data, entry) {
		return false, nil
	}

	var out bytes.Buffer
	out.Write(data)
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		out.WriteByte('\n')
	}
	// Blank line before the section so it visually separates from prior entries
	// in a non-empty file. Skip for fresh files (leading blank line looks odd).
	if len(data) > 0 {
		out.WriteByte('\n')
	}
	out.WriteString(gitignoreComment)
	out.WriteByte('\n')
	out.WriteString(entry)
	out.WriteByte('\n')

	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return false, fmt.Errorf("write .gitignore: %w", err)
	}
	return true, nil
}

func containsGitignoreEntry(content []byte, entry string) bool {
	target := normalizeGitignorePattern(entry)
	for _, raw := range strings.Split(string(content), "\n") {
		stripped := strings.TrimSpace(raw)
		if idx := strings.Index(stripped, "#"); idx >= 0 {
			stripped = strings.TrimSpace(stripped[:idx])
		}
		if stripped == "" {
			continue
		}
		if normalizeGitignorePattern(stripped) == target {
			return true
		}
	}
	return false
}

func normalizeGitignorePattern(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimSuffix(s, "/")
	return s
}

// guardrailMarker is the idempotency sentinel for the Springfield agent
// guardrail block. Its presence means the block is already installed and
// Springfield will not re-append.
const guardrailMarker = "<!-- springfield:guardrail -->"

// guardrailBlock is the exact text appended (with trailing newline) to
// CLAUDE.md / AGENTS.md. Deliberately minimal so it coexists with whatever
// project-specific guidance the host repo maintains.
const guardrailBlock = guardrailMarker + `
## Springfield control plane

Never read, write, edit, or delete files under ` + "`.springfield/`" + `. That directory is Springfield's internal state. Writing to it will abort the current run.
`

// ensureGuardrailBlock appends the Springfield guardrail block to the given
// agent-instruction file when the idempotency marker is absent. Creates the
// file (with a simple header) when missing. Returns (added, err) where
// added==true means the block was just written.
//
// The write uses writeFileReplacingNonRegular (temp + fsync + rename) so a
// crash mid-write cannot leave CLAUDE.md / AGENTS.md truncated or empty —
// the rename is atomic. The existing file's mode is preserved; fresh files
// default to 0o644.
func ensureGuardrailBlock(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}

	if bytes.Contains(data, []byte(guardrailMarker)) {
		return false, nil
	}

	var buf bytes.Buffer
	if len(data) == 0 {
		// Fresh file: lead with a minimal project header so the guardrail
		// isn't the very first line with nothing above it.
		buf.WriteString("# Agent Instructions\n\n")
	} else {
		buf.Write(data)
		if !bytes.HasSuffix(data, []byte("\n")) {
			buf.WriteByte('\n')
		}
		buf.WriteByte('\n')
	}
	buf.WriteString(guardrailBlock)

	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}

	if err := writeFileReplacingNonRegular(path, buf.Bytes(), mode); err != nil {
		return false, fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return true, nil
}

