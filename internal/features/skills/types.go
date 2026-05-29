package skills

import (
	"fmt"
	"strings"

	"springfield/internal/features/playbooks"
)

// Name identifies one Springfield-owned skill contract.
type Name string

const (
	SkillPlan    Name = "plan"
	SkillStatus  Name = "status"
	SkillRecover Name = "recover"
)

// MinCLIVersion is the generous, hand-maintained floor: the oldest springfield
// CLI these skills are known to work against. Bump it manually — and rarely —
// only when a skill starts depending on a newer CLI capability. It is NOT
// auto-synced to version.txt; cmd/regen does not touch it. The asymmetry is
// intentional: the plugin updates lag the CLI (manual on both Claude Code and
// Codex), so "older plugin + newer CLI" is the normal steady state and must
// stay silent. The floor only fires for the rarer "plugin needs a newer CLI"
// direction, where the remediation is `brew upgrade springfield`.
const MinCLIVersion = "0.11.0"

// versionCheckPreamble is prepended to every skill body so the agent verifies
// the CLI is present and recent enough before doing any Springfield work, and
// surfaces an actionable install/upgrade command instead of a cryptic failure.
var versionCheckPreamble = strings.TrimSpace(`
## Before you start — verify the Springfield CLI

Run ` + "`springfield version`" + ` first. It prints one of:
- ` + "`springfield vX.Y.Z`" + ` — a released build.
- ` + "`springfield dev`" + ` — a local source build (e.g. ` + "`go install .`" + `).

Then:

- If the command is **not found**, the CLI is not installed. Tell the user to install it, then stop:
  - macOS: ` + "`brew install brentguistwite/tap/springfield`" + `
  - Linux: download the matching ` + "`springfield_<version>_linux_<arch>.tar.gz`" + ` from the GitHub Releases page and put the ` + "`springfield`" + ` binary on PATH.
  - Windows: build from source with ` + "`go install .`" + ` inside the Springfield repo (no Windows release tarballs are published yet).
- If the output is ` + "`springfield dev`" + `, this is a local development build. Continue without a floor check — the user is responsible for keeping it current.
- If the reported version is **older than v` + MinCLIVersion + `** (compare semver-style after stripping the leading ` + "`v`" + ` from the reported version), tell the user to upgrade, then stop:
  - macOS: ` + "`brew upgrade springfield`" + `
  - Linux: download the latest ` + "`springfield_<version>_linux_<arch>.tar.gz`" + ` from the GitHub Releases page and replace the binary on PATH.
  - Windows: ` + "`go install .`" + ` inside the Springfield repo (no Windows release tarballs yet).
- Otherwise continue.

Do not try to work around a missing or too-old CLI — surface the exact command above instead. (A plugin older than the CLI is fine and needs no action; the CLI stays backward-compatible with older skills within a major version.)

## Springfield control plane

**Reads are allowed** — recover and status flows specifically inspect ` + "`.springfield/run.json`" + ` and per-plan ` + "`prd.json`" + `. **Never write, edit, or delete** files under ` + "`.springfield/`" + `. That directory is Springfield's state — the CLI is your only interface for mutating it. Writing there directly will abort the current batch. This applies regardless of which agent is invoking the skill.

---
`)

// Skill describes one canonical Springfield skill file.
type Skill struct {
	Name         Name
	Summary      string
	Purpose      playbooks.Purpose
	Header       string
	Description  string
	RelativePath string
	TaskBody     string
}

// LocalTarget describes one local sync destination used by the helper CLI.
type LocalTarget struct {
	Name         string
	Summary      string
	Purpose      playbooks.Purpose
	TaskBody     string
	Header       string
	Description  string
	RelativePath string
}

// Rendered is the resolved prompt plus checked-in skill file content.
type Rendered struct {
	Skill   Skill
	Prompt  string
	Content string
}

// Installed describes one written local host artifact file.
type Installed struct {
	Host LocalTarget
	Path string
}

// InstallOptions controls where Springfield writes local host artifacts.
type InstallOptions struct {
	Hosts     []string
	ClaudeDir string
	CodexDir  string
}

var skillCatalog = []Skill{
	{
		Name:         SkillPlan,
		Summary:      "Compile a Springfield plan into a runnable batch.",
		Purpose:      playbooks.PurposePlan,
		Header:       "Springfield Plan",
		Description:  "Use Springfield plan to compile a new work request into a runnable batch for the current project.",
		RelativePath: "skills/plan/SKILL.md",
		TaskBody: versionCheckPreamble + "\n\n" + strings.TrimSpace(`
Compile a Springfield batch from the user's work request.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

## Step 1 — Determine source

Ask the user whether they have an existing plan file or want to describe the work directly:

1. **Existing plan file**: ask for the file path, then read it.
2. **Fresh prompt**: ask the user to describe what they want to build.

Do not infer file-vs-prompt from one ambiguous input.

## Step 2 — Check for active batch

Run ` + "`springfield status`" + ` to check whether an active batch already exists.

- If an active batch exists and any slice is ` + "`running`" + `, tell the user to wait before replacing.
- If an active batch exists but nothing is running, ask the user: replace it, append to it, or keep it.

## Step 3 — Read and slice the plan

If the user pointed to a file, read it. If prompt-mode, treat the prompt as source.

Decide slice boundaries based on the plan's meaning, not syntax. A slice should:

- Be independently deliverable in one agent run.
- Map to one coherent outcome (e.g., "scaffold auth package", "wire login endpoint").
- Not span unrelated subsystems.

Markdown cues to consider (in priority order):

1. Explicit ` + "`## Task N:`" + ` / ` + "`## Step N:`" + ` headers — honor them.
2. H2/H3 sections that each describe a discrete deliverable.
3. Numbered lists of implementation steps.
4. Prose plans — chunk by responsibility.

If the plan is genuinely one step, emit one slice. Don't pad.

## Step 4 — Confirm and persist

Show the user the proposed plans (title + one-line intent per plan).
Ask for confirmation before writing.

Once confirmed, compile a **PRD envelope** and pipe it to ` + "`springfield plan --prd -`" + `:

` + "```" + `bash
springfield plan --prd - <<'JSON'
{
  "title": "<batch title>",
  "source": "<original plan text, verbatim>",
  "phases": [
    {"mode": "serial", "plans": ["plan-01", "plan-02"]}
  ],
  "plans": [
    {
      "id": "plan-01",
      "title": "<plan 1 title>",
      "description": "<plan 1 description>",
      "context_md": "Project uses TypeScript + Bun. Follow existing test patterns.",
      "user_stories": [
        {
          "id": "US-001",
          "title": "<story title>",
          "description": "<story description>",
          "acceptance_criteria": ["<criterion 1>"],
          "priority": 1,
          "passes": false,
          "deps": []
        }
      ]
    },
    {
      "id": "plan-02",
      "title": "<plan 2 title>",
      "description": "<plan 2 description>",
      "context_md": "Project uses TypeScript + Bun. Follow existing test patterns.",
      "user_stories": [
        {
          "id": "US-001",
          "title": "<story title>",
          "description": "<story description>",
          "acceptance_criteria": ["<criterion 1>"],
          "priority": 1,
          "passes": false,
          "deps": []
        }
      ]
    }
  ]
}
JSON
` + "```" + `

Schema notes:
- ` + "`phases`" + `: execution ordering. Each phase has ` + "`mode`" + ` (` + "`\"serial\"`" + ` or ` + "`\"parallel\"`" + `) and ` + "`plans`" + ` (list of plan IDs in that phase).
- ` + "`plans`" + `: each plan has ` + "`id`" + `, ` + "`title`" + `, ` + "`description`" + `, optional ` + "`context_md`" + `, and ` + "`user_stories`" + `.
- ` + "`context_md`" + `: plan-specific context only. Project-wide guidance (build commands, repo conventions) lives in root ` + "`AGENTS.md`" + ` and is auto-loaded by the runner — do not duplicate it into ` + "`context_md`" + ` or you double the prompt-token cost of that material every iteration.
- ` + "`user_stories`" + `: each story has ` + "`id`" + ` (` + "`US-NNN`" + `), ` + "`title`" + `, ` + "`description`" + `, ` + "`acceptance_criteria`" + `, ` + "`priority`" + ` (int, lower = runs first), ` + "`passes`" + ` (false initially), ` + "`deps`" + ` (story IDs within same plan).
- See ` + "`docs/prd-format.md`" + ` for full field semantics, validation rules, and stop conditions.

> **Constraint — documentation acceptance criteria must name an explicit target file.**
> Any ` + "`acceptance_criteria`" + ` entry that prescribes writing or updating documentation MUST name the exact target as a ` + "`path/to/file.md`" + ` (add a ` + "`:line`" + ` anchor when pointing at an existing section). If no canonical file exists yet, the criterion must say to create it at a named path.
> Rationale: in a dogfood batch an agent burned ~75 turns hunting for a "review docs" file that never existed because the criterion never named one — vague targets cause thrash.
> - Allowed: ` + "`\"Document the off-target marker rule in docs/prd-format.md under the 'Stop conditions' section\"`" + `
> - Forbidden: ` + "`\"Document the off-target marker rule in the review docs\"`" + ` and ` + "`\"note this in the relevant section\"`" + ` — no file path, so the agent has nothing concrete to target.

Use ` + "`--replace`" + ` or ` + "`--append`" + ` if an active batch exists (per Step 2).

Keep Springfield as the only user-facing surface.
`),
	},
	{
		Name:         SkillStatus,
		Summary:      "Inspect the active Springfield batch and explain where it stands.",
		Purpose:      playbooks.PurposeStatus,
		Header:       "Springfield Status",
		Description:  "Use Springfield status to inspect the active batch and explain where it stands.",
		RelativePath: "skills/status/SKILL.md",
		TaskBody: versionCheckPreamble + "\n\n" + strings.TrimSpace(`
Inspect the current Springfield batch for the project and report the current state.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

Run ` + "`springfield status`" + ` to get the current Springfield batch state, then summarize:
- The active batch id and title
- The current phase
- Which slices are done, running, blocked, or queued
- Per-plan story progress: e.g. "Plan 03: 5/8 stories pass; failed at US-006"
- The last known error if any
- The clearest next action for the user

Do not invent new work unless the user explicitly asks to re-plan it.
Keep Springfield as the only user-facing surface.
`),
	},
	{
		Name:         SkillRecover,
		Summary:      "Diagnose a stuck batch or failed slice and restore a safe next step.",
		Purpose:      playbooks.PurposeRecover,
		Header:       "Springfield Recover",
		Description:  "Use Springfield recover to diagnose a stuck batch or failed slice and restore a safe next step.",
		RelativePath: "skills/recover/SKILL.md",
		TaskBody: versionCheckPreamble + "\n\n" + strings.TrimSpace(`
Recover a Springfield batch that is stalled, blocked, or has a failed slice.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

## Step 1 — Read current state

Run ` + "`springfield status`" + ` to see the active batch, current phase, and slice statuses.

For a failing or stalled plan, run ` + "`springfield recover --diagnose --plan <plan-id>`" + ` first. The CLI surfaces dirty-worktree state, ` + "`exit_reason`" + `, orphan-plan handling, and the available recovery actions — interpret that output rather than re-deriving it from raw files.

Also read ` + "`.springfield/run.json`" + ` for the last checkpoint and last known error.

## Step 2 — Diagnose

Identify which plan and which story failed or stalled. Check:
- The CLI diagnose output (above) — primary source.
- The last error in ` + "`run.json`" + `
- Per-story ` + "`passes`" + ` state in each plan's ` + "`prd.json`" + ` (` + "`.springfield/plans/<plan-id>/prd.json`" + `)
- Any blockers mentioned in the batch source

## Step 3 — Recover

Propose the safest concrete next step:
- For a failed plan: fix the underlying issue, then run ` + "`springfield start`" + ` to resume from cursor. Already-passed stories (` + "`passes: true`" + `) are skipped on re-entry — ` + "`prd.json`" + ` is preserved across retries.
- For a blocked plan: explain what needs to happen before execution can continue.
- For a corrupt batch: use ` + "`springfield plan --replace`" + ` to start fresh with a new batch.

Story-level retry is not supported in v1; recovery operates at plan grain (retry plan or retry merge).

Prefer recovery and continuation over starting a fresh plan unless the existing state cannot be salvaged.
Keep Springfield as the only user-facing surface.
`),
	},
}

var localTargets = []LocalTarget{
	{
		Name:         "claude-code",
		Summary:      "Springfield local helper command for Claude Code.",
		Purpose:      playbooks.PurposePlan,
		Header:       "Springfield Command",
		Description:  "Use `/springfield` in Claude Code to run Springfield inside this project.",
		RelativePath: "springfield.md",
		TaskBody: strings.TrimSpace(`
Use Springfield as the primary agent-native surface for this project.

Keep Springfield as the only user-facing surface.
If the user asks what Springfield does, explain the current project context and Springfield guidance before planning or execution.
When work is requested, ask clarifying questions when needed, then drive toward a concrete Springfield work definition with named workstreams.
Stay aligned with the shared Springfield playbook and the current project's guidance.

Control plane: reads are allowed for diagnosis; never write, edit, or delete files under the .springfield/ directory. The Springfield CLI is the only interface for mutating that state.
`),
	},
	{
		Name:         "codex",
		Summary:      "Springfield local helper skill for Codex.",
		Purpose:      playbooks.PurposePlan,
		Header:       "Springfield Skill",
		Description:  "Use the Springfield skill in Codex to run Springfield inside this project.",
		RelativePath: "springfield/SKILL.md",
		TaskBody: strings.TrimSpace(`
Use Springfield as the primary agent-native surface for this project.

Keep Springfield as the only user-facing surface.
If the user asks what Springfield does, explain the current project context and Springfield guidance before planning or execution.
When work is requested, ask clarifying questions when needed, then drive toward a concrete Springfield work definition with named workstreams.
Stay aligned with the shared Springfield playbook and the current project's guidance.

Control plane: reads are allowed for diagnosis; never write, edit, or delete files under the .springfield/ directory. The Springfield CLI is the only interface for mutating that state.
`),
	},
}

// Catalog returns the canonical Springfield skill catalog.
func Catalog() []Skill {
	out := make([]Skill, len(skillCatalog))
	copy(out, skillCatalog)
	return out
}

// Lookup resolves one canonical Springfield skill.
func Lookup(name string) (Skill, error) {
	for _, skill := range skillCatalog {
		if string(skill.Name) == name {
			return skill, nil
		}
	}
	return Skill{}, fmt.Errorf("unknown Springfield skill %q", name)
}

func localCatalog() []LocalTarget {
	out := make([]LocalTarget, len(localTargets))
	copy(out, localTargets)
	return out
}
