package cmd

import (
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

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/catalog"
	"springfield/internal/core/config"
)

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

			if added, err := ensureSpringfieldGitignore(dir); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to update .gitignore: %v\n", err)
			} else if added {
				fmt.Fprintln(cmd.OutOrStdout(), "Added Springfield patterns to .gitignore")
			}

			if err := ensureAgentInstructionFiles(cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, priority); err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout())
			printNextSteps(cmd.OutOrStdout(), isTTY(int(os.Stdout.Fd())))

			return nil
		},
	}

	cmd.Flags().StringVar(&agentsFlag, "agents", "", "Comma-separated agent priority list (e.g. claude,codex)")
	cmd.Flags().StringVar(&modelsFlag, "model", "", "Comma-separated per-agent model overrides (e.g. claude=claude-sonnet-4-6,codex=gpt-5-codex)")
	cmd.Flags().BoolVar(&resetFlag, "reset", false, "Back up existing config and rewrite from scratch (destructive)")

	return cmd
}

// resolveInitSelections is the single decision point for how `springfield init`
// learns the operator's agent + model preferences. Three paths:
//
//   - Flags: --agents and (optionally) --model bypass the form entirely.
//   - Interactive TTY: hand off to the huh form for back-nav and a confirm
//     screen.
//   - Non-TTY (CI / piped install): huh's accessible mode renders the same
//     fields as numbered plain-text prompts driven from stdin line-by-line.
func resolveInitSelections(
	agentsFlag string,
	modelsFlag string,
	interactive bool,
	in io.Reader,
	out io.Writer,
	suggest func(agents.ID) []string,
) ([]string, map[string]string, error) {
	if agentsFlag != "" {
		priority, err := parseAndValidateAgents(agentsFlag)
		if err != nil {
			return nil, nil, err
		}
		var models map[string]string
		if modelsFlag != "" {
			models, err = parseAndValidateModels(modelsFlag, priority)
			if err != nil {
				return nil, nil, err
			}
		}
		return priority, models, nil
	}

	det := newRegistryDetector(exec.LookPath)
	return runInitForm(in, out, det, suggest, !interactive)
}

// Detector reports detection status for execution-supported agents. Exported
// so external test packages (tests/cmd) can supply fakes without touching
// internals.
type Detector interface {
	Detect(id agents.ID) agents.DetectionStatus
}

// printNextSteps writes the post-init next-step copy. lipgloss styling is
// applied only when stdout is a TTY — pipes and CI logs get plain text.
func printNextSteps(w io.Writer, tty bool) {
	header := "Next:"
	next := "springfield plan"
	then := "Then: springfield start"
	if tty {
		bold := lipgloss.NewStyle().Bold(true)
		fmt.Fprintln(w, bold.Render(header)+" "+next)
		fmt.Fprintln(w, then)
		return
	}
	fmt.Fprintln(w, header+" "+next)
	fmt.Fprintln(w, then)
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

const springfieldGitignoreBlock = `# Springfield — plans tracked; runtime state local-only
.springfield/*
!.springfield/plans/
.springfield/plans/*/
`

// ensureSpringfieldGitignore writes the selective Springfield gitignore
// block to <dir>/.gitignore. Creates the file when missing. Idempotent:
// skips when ".springfield/*" is already present. Replaces the old blanket
// ".springfield/" pattern if found (directory-level ignore prevents child
// un-ignores from working).
func ensureSpringfieldGitignore(dir string) (added bool, err error) {
	path := filepath.Join(dir, ".gitignore")

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("read .gitignore: %w", err)
	}

	if bytes.Contains(data, []byte(".springfield/*")) {
		return false, nil
	}

	cleaned := stripGitignoreLine(data, ".springfield")

	var out bytes.Buffer
	out.Write(cleaned)
	if len(cleaned) > 0 && !bytes.HasSuffix(cleaned, []byte("\n")) {
		out.WriteByte('\n')
	}
	if len(cleaned) > 0 {
		out.WriteByte('\n')
	}
	out.WriteString(springfieldGitignoreBlock)

	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return false, fmt.Errorf("write .gitignore: %w", err)
	}
	return true, nil
}

// stripGitignoreLine removes lines whose normalized pattern matches target.
func stripGitignoreLine(data []byte, normalized string) []byte {
	lines := strings.Split(string(data), "\n")
	var out []string
	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		if idx := strings.Index(stripped, "#"); idx >= 0 {
			stripped = strings.TrimSpace(stripped[:idx])
		}
		if stripped != "" && normalizeGitignorePattern(stripped) == normalized {
			continue
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
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

// agentInstructionFile is the per-agent canonical filename. AGENTS.md is the
// shared default (also Codex's native file); CLAUDE.md / GEMINI.md are
// the Claude / Gemini conventions.
var agentInstructionFile = map[agents.ID]string{
	agents.AgentClaude: "CLAUDE.md",
	agents.AgentCodex:  "AGENTS.md",
	agents.AgentGemini: "GEMINI.md",
}

// canonicalCandidates is the lookup order for an existing canonical file.
// AGENTS.md wins because it's the broadest convention and Codex's native
// home; the others are honored if the operator already adopted them as
// their single source of truth.
var canonicalCandidates = []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"}

// ensureAgentInstructionFiles writes one canonical agent-instruction file
// (AGENTS.md by default, or whichever already exists) and creates symlinks
// from the other selected agents' filenames pointing at it. The guardrail
// block is appended to the canonical file (idempotent).
//
// Existing real (non-symlink) files are respected: if CLAUDE.md is already a
// regular file with content, it's left in place and gets the guardrail
// directly — the operator's setup is not silently rewritten.
func ensureAgentInstructionFiles(stdout, stderr io.Writer, dir string, priority []string) error {
	canonical := pickCanonical(dir)
	canonicalPath := filepath.Join(dir, canonical)

	added, err := ensureGuardrailBlock(canonicalPath)
	if err != nil {
		fmt.Fprintf(stderr, "warning: failed to update %s: %v\n", canonical, err)
	} else if added {
		fmt.Fprintf(stdout, "Added Springfield guardrail to %s\n", canonical)
	}

	seenTargets := map[string]bool{canonical: true}
	for _, id := range priority {
		name, ok := agentInstructionFile[agents.ID(id)]
		if !ok || seenTargets[name] {
			continue
		}
		seenTargets[name] = true

		path := filepath.Join(dir, name)
		if _, lerr := os.Lstat(path); lerr == nil {
			// Already exists in some form (real file or symlink). Append the
			// guardrail (ensureGuardrailBlock follows symlinks to the
			// canonical target, so duplicate writes are no-ops).
			if added, gerr := ensureGuardrailBlock(path); gerr != nil {
				fmt.Fprintf(stderr, "warning: failed to update %s: %v\n", name, gerr)
			} else if added {
				fmt.Fprintf(stdout, "Added Springfield guardrail to %s\n", name)
			}
			continue
		}

		// Missing: create as relative symlink to canonical so editors and
		// agents see consistent content via either path.
		if err := os.Symlink(canonical, path); err != nil {
			fmt.Fprintf(stderr, "warning: failed to symlink %s -> %s: %v\n", name, canonical, err)
			continue
		}
		fmt.Fprintf(stdout, "Linked %s -> %s\n", name, canonical)
	}

	return nil
}

// pickCanonical returns the existing canonical agent-instruction filename in
// dir (honoring whichever file the operator already adopted) or the default
// "AGENTS.md" when none exist. Existence is checked with Lstat so a symlink
// counts as "present" — operating on the symlink resolves to its target via
// ensureGuardrailBlock's symlink handling.
func pickCanonical(dir string) string {
	for _, name := range canonicalCandidates {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return name
		}
	}
	return "AGENTS.md"
}

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
	// Resolve symlinks before any read/write. The keep-agents-md-canonical
	// convention has CLAUDE.md / AGENTS.md / GEMINI.md as symlinks pointing at
	// a single source of truth. Operating on the resolved target preserves
	// the operator's setup; without this, writeFileReplacingNonRegular would
	// see a non-regular node and replace the symlink with a regular file.
	if info, lerr := os.Lstat(path); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
		if resolved, rerr := filepath.EvalSymlinks(path); rerr == nil {
			path = resolved
		}
	}

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
