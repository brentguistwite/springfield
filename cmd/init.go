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
	"springfield/internal/features/execution"
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
	var trackedGitignoreFlag bool
	var branchModeFlag string
	var baseBranchFlag string
	modelSuggester := newModelSuggester(exec.LookPath)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new Springfield project in the current directory.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}

			branchMode, err := validateBranchMode(branchModeFlag)
			if err != nil {
				return err
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
				Reset:      resetFlag,
				Models:     models,
				BranchMode: branchMode,
				BaseBranch: strings.TrimSpace(baseBranchFlag),
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

			// Bootstrap the execution config so the project is immediately
			// loadable by every read path (status, plan --dry-run, plans list).
			// Without this, those commands fail on a missing
			// execution/config.json until a mutating command (plan/plans add)
			// lazily creates it — the first-run gap this repairs. Idempotent:
			// an existing config is reused unchanged, so re-running init also
			// heals a project left half-initialized by an older build.
			if err := execution.EnsureExecutionConfig(dir); err != nil {
				return fmt.Errorf("bootstrap execution config: %w", err)
			}
			// Reconcile config.json's primary tool with the chosen agent
			// priority. EnsureExecutionConfig reuses an existing config
			// unchanged, so a re-init that reorders agents (merge mode or
			// --reset) would otherwise leave a stale tool behind. Lossless:
			// preserves plan_units and other runtime settings.
			if err := execution.SyncExecutionTool(dir); err != nil {
				return fmt.Errorf("sync execution config tool: %w", err)
			}

			// Ignore setup. Default is team-repo-safe: patterns land in
			// .git/info/exclude (untracked, per-clone), so a repo the operator
			// does not own keeps its tracked .gitignore byte-unchanged.
			// --tracked-gitignore opts into the legacy behavior of editing the
			// tracked .gitignore, for repos the operator owns. Both paths are
			// best-effort — a warning on failure never aborts init (e.g. the
			// exclude writer skips when dir is not yet a git repo).
			if trackedGitignoreFlag {
				if added, err := ensureSpringfieldGitignore(dir); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to update .gitignore: %v\n", err)
				} else if added {
					fmt.Fprintln(cmd.OutOrStdout(), "Added Springfield patterns to .gitignore")
				}
			} else {
				if added, err := config.EnsureSpringfieldExclude(dir); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to update .git/info/exclude: %v\n", err)
				} else if added {
					fmt.Fprintln(cmd.OutOrStdout(), "Added Springfield patterns to .git/info/exclude")
				}
			}

			fmt.Fprintln(cmd.OutOrStdout())
			printNextSteps(cmd.OutOrStdout(), isTTY(int(os.Stdout.Fd())))

			return nil
		},
	}

	cmd.Flags().StringVar(&agentsFlag, "agents", "", "Comma-separated agent priority list (e.g. claude,codex)")
	cmd.Flags().StringVar(&modelsFlag, "model", "", "Comma-separated per-agent model overrides (e.g. claude=claude-sonnet-4-6,codex=gpt-5-codex)")
	cmd.Flags().BoolVar(&resetFlag, "reset", false, "Regenerate springfield.toml from the current --agents/--model selection, backing up the previous file. Discards manual edits and stale agent blocks; the execution config's primary tool is updated to match, and registered plans are preserved.")
	cmd.Flags().BoolVar(&trackedGitignoreFlag, "tracked-gitignore", false, "Write Springfield ignore patterns to the tracked .gitignore instead of the default .git/info/exclude. Use in repos you own; the default keeps a repo's tracked .gitignore byte-unchanged.")
	cmd.Flags().StringVar(&branchModeFlag, "branch-mode", "", "Default branch mode for multi-plan batches: consolidate (merge each plan into a shared base) or per-plan (one branch per plan). Written to [project] branch_mode.")
	cmd.Flags().StringVar(&baseBranchFlag, "base-branch", "", "Default base branch each plan branch is cut from in per-plan mode. Written to [project] base_branch.")

	return cmd
}

// validateBranchMode normalizes and validates the --branch-mode flag. Empty is
// allowed (the key is omitted / left untouched). A non-empty value must be one
// of the config.BranchMode constants; anything else is a hard error so the CLI
// exits non-zero rather than persisting an unusable branch_mode.
func validateBranchMode(raw string) (string, error) {
	mode := strings.TrimSpace(raw)
	switch mode {
	case "", string(config.BranchModeConsolidate), string(config.BranchModePerPlan):
		return mode, nil
	default:
		return "", fmt.Errorf(
			"invalid --branch-mode %q: want %q or %q",
			mode, config.BranchModeConsolidate, config.BranchModePerPlan,
		)
	}
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
//
// The signpost points at the Springfield "plan" skill rather than the bare
// `springfield plan` CLI verb: `springfield plan` requires a compiled PRD
// envelope (--prd), which the skill authors for you. Copy stays
// platform-neutral because slash syntax differs per agent (Claude Code:
// `/springfield:plan`; Codex invokes the same skill via its own UI).
func printNextSteps(w io.Writer, tty bool) {
	header := "Next:"
	next := `run the Springfield "plan" skill in your agent (Claude Code: /springfield:plan) to draft a PRD`
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

// springfieldGitignoreBlock ignores Springfield's local-only state. The final
// line ignores config.LocalFileName ("springfield.local.toml"), the per-operator
// review override that sits at the project root (outside .springfield/) and must
// never be committed. Kept literal because this is a raw-string const.
const springfieldGitignoreBlock = `# Springfield — plans tracked; runtime state local-only
.springfield/*
!.springfield/plans/
.springfield/plans/*/
springfield.local.toml
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
