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
	var modelsFlag string
	var resetFlag bool
	modelSuggester := newModelSuggester(exec.LookPath)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new Springfield project in the current directory.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}

			interactive := isTTY(int(os.Stdin.Fd()))
			priority, models, err := resolveInitSelections(
				agentsFlag,
				modelsFlag,
				interactive,
				cmd.InOrStdin(),
				cmd.OutOrStdout(),
				modelSuggester,
			)
			if err != nil {
				return err
			}

			result, err := config.Init(dir, priority, config.InitOptions{
				Reset:  resetFlag,
				Models: models,
			})
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
	cmd.Flags().StringVar(&modelsFlag, "model", "", "Comma-separated per-agent model overrides (e.g. claude=claude-sonnet-4-6,codex=gpt-5-codex)")
	cmd.Flags().BoolVar(&resetFlag, "reset", false, "Back up existing config and rewrite from scratch (destructive)")

	return cmd
}

func resolveInitSelections(
	agentsFlag string,
	modelsFlag string,
	interactive bool,
	in io.Reader,
	out io.Writer,
	suggest func(agents.ID) []string,
) ([]string, map[string]string, error) {
	reader := bufio.NewReader(in)

	priority, err := resolvePriorityWithReader(agentsFlag, interactive, reader, out)
	if err != nil {
		return nil, nil, err
	}

	models, err := resolveModelsWithReader(
		modelsFlag,
		interactive,
		priority,
		reader,
		out,
		suggest,
	)
	if err != nil {
		return nil, nil, err
	}

	return priority, models, nil
}

// resolvePriority determines the agent priority list from flag or interactive
// prompt. Non-interactive callers must pass --agents explicitly — there is no
// fixed default priority for fresh init.
func resolvePriority(agentsFlag string, interactive bool, in io.Reader, out io.Writer) ([]string, error) {
	return resolvePriorityWithReader(agentsFlag, interactive, bufferedReader(in), out)
}

func resolvePriorityWithReader(agentsFlag string, interactive bool, in *bufio.Reader, out io.Writer) ([]string, error) {
	if agentsFlag != "" {
		return parseAndValidateAgents(agentsFlag)
	}

	if !interactive {
		return nil, fmt.Errorf(
			"non-interactive init requires --agents flag (e.g. --agents claude,codex,gemini)")
	}

	return promptForAgentsWithReader(in, out)
}

func resolveModels(
	agentsFlag string,
	modelsFlag string,
	interactive bool,
	priority []string,
	in io.Reader,
	out io.Writer,
	suggest func(agents.ID) []string,
) (map[string]string, error) {
	return resolveModelsWithReader(modelsFlag, interactive, priority, bufferedReader(in), out, suggest)
}

func resolveModelsWithReader(
	modelsFlag string,
	interactive bool,
	priority []string,
	in *bufio.Reader,
	out io.Writer,
	suggest func(agents.ID) []string,
) (map[string]string, error) {
	if modelsFlag != "" {
		return parseAndValidateModels(modelsFlag, priority)
	}

	if !interactive {
		return nil, nil
	}

	enabled := make([]agents.ID, 0, len(priority))
	for _, id := range priority {
		enabled = append(enabled, agents.ID(id))
	}

	models, err := promptForAgentModelsWithReader(in, out, enabled, suggest)
	if err != nil {
		return nil, err
	}

	if len(models) == 0 {
		return map[string]string{}, nil
	}

	result := make(map[string]string, len(models))
	for id, model := range models {
		result[string(id)] = model
	}

	return result, nil
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
	return promptForAgentsWithDetectionReader(bufferedReader(in), out, det)
}

func promptForAgentsWithDetectionReader(in *bufio.Reader, out io.Writer, det Detector) ([]string, error) {
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

	for attempt := 0; attempt < maxPromptAttempts; attempt++ {
		fmt.Fprint(out, "Enter agents in priority order (comma-separated, e.g. claude,codex): ")

		line, err := in.ReadString('\n')
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

// PromptForAgentModels prompts once per enabled agent for an optional model
// override. Blank input keeps the adapter default and is omitted from the
// returned map.
func PromptForAgentModels(
	in io.Reader,
	out io.Writer,
	enabled []agents.ID,
	suggest func(agents.ID) []string,
) (map[agents.ID]string, error) {
	return promptForAgentModelsWithReader(bufferedReader(in), out, enabled, suggest)
}

func promptForAgentModelsWithReader(
	in *bufio.Reader,
	out io.Writer,
	enabled []agents.ID,
	suggest func(agents.ID) []string,
) (map[agents.ID]string, error) {
	models := make(map[agents.ID]string, len(enabled))

	for _, id := range enabled {
		suggestions := suggest(id)
		fmt.Fprintf(
			out,
			"Model for %s (blank = default; suggestions: %s): ",
			id,
			strings.Join(suggestions, ", "),
		)

		line, err := in.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("read input: %w", err)
		}

		model := strings.TrimSpace(line)
		if model != "" {
			models[id] = model
		}

		if errors.Is(err, io.EOF) {
			break
		}
	}

	return models, nil
}

// promptForAgents runs the interactive picker against real adapter detection.
// Internal wrapper around PromptForAgentsWithDetection — it constructs a
// production registryDetector backed by os/exec.LookPath so call sites in
// resolvePriority don't need to know about the Detector seam.
func promptForAgents(in io.Reader, out io.Writer) ([]string, error) {
	return promptForAgentsWithReader(bufferedReader(in), out)
}

func promptForAgentsWithReader(in *bufio.Reader, out io.Writer) ([]string, error) {
	return promptForAgentsWithDetectionReader(in, out, newRegistryDetector(exec.LookPath))
}

func bufferedReader(in io.Reader) *bufio.Reader {
	if reader, ok := in.(*bufio.Reader); ok {
		return reader
	}
	return bufio.NewReader(in)
}

func newModelSuggester(lookPath agents.LookPathFunc) func(agents.ID) []string {
	registry := agents.NewRegistry(catalog.DefaultAdapters(lookPath)...)
	return newModelSuggesterFromRegistry(registry)
}

func newModelSuggesterFromRegistry(registry agents.Registry) func(agents.ID) []string {
	return func(id agents.ID) []string {
		resolved, err := registry.Resolve(agents.ResolveInput{ProjectDefault: id})
		if err != nil {
			panic(fmt.Sprintf("impossible state: no adapter registered for agent %q", id))
		}

		provider, ok := resolved.Adapter.(agents.ModelProvider)
		if !ok {
			return nil
		}

		return provider.SuggestedModels()
	}
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

func parseAndValidateModels(raw string, priority []string) (map[string]string, error) {
	enabled := make(map[string]struct{}, len(priority))
	for _, id := range priority {
		enabled[id] = struct{}{}
	}

	models := make(map[string]string)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		id, model, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --model entry %q: want agent=model", part)
		}

		id = strings.TrimSpace(id)
		model = strings.TrimSpace(model)
		if id == "" {
			return nil, fmt.Errorf("invalid --model entry %q: missing agent", part)
		}
		if !agents.IsExecutionSupported(agents.ID(id)) {
			return nil, fmt.Errorf("%s is not yet supported for execution", id)
		}
		if _, ok := enabled[id]; !ok {
			return nil, fmt.Errorf("--model agent %q not present in --agents priority", id)
		}
		if _, dup := models[id]; dup {
			return nil, fmt.Errorf("duplicate model override for agent %q", id)
		}

		models[id] = model
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("at least one agent=model entry is required in --model")
	}

	return models, nil
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
