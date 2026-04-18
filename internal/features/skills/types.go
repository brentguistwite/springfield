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
	SkillStart   Name = "start"
	SkillStatus  Name = "status"
	SkillRecover Name = "recover"
)

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
		TaskBody: strings.TrimSpace(`
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

Show the user the proposed slice list (title + one-line intent per slice).
Ask for confirmation before writing.

Once confirmed, pipe a JSON payload to ` + "`springfield plan --slices -`" + `:

` + "```" + `bash
springfield plan --slices - <<'JSON'
{
  "title": "<batch title>",
  "source": "<original plan markdown, verbatim>",
  "slices": [
    {"id": "01", "title": "<slice 1 title>", "summary": "<slice 1 body>"},
    {"id": "02", "title": "<slice 2 title>", "summary": "<slice 2 body>"}
  ]
}
JSON
` + "```" + `

Slice IDs: zero-padded (` + "`01`" + `, ` + "`02`" + `, ...). Title short; summary is the actionable body for the slice.

Use ` + "`--replace`" + ` or ` + "`--append`" + ` if an active batch exists (per Step 2).

Keep Springfield as the only user-facing surface.
`),
	},
	{
		Name:         SkillStart,
		Summary:      "Execute the active Springfield batch for the current project from its saved progress.",
		Purpose:      playbooks.PurposeStart,
		Header:       "Springfield Start",
		Description:  "Use Springfield start to execute the active batch for the current project from its saved progress.",
		RelativePath: "skills/start/SKILL.md",
		TaskBody: strings.TrimSpace(`
Execute the active Springfield batch for the current project.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

## Step 1 — Check for active batch

Run ` + "`springfield status`" + ` to confirm an active batch exists and review its saved progress.

If no active batch exists, stop and tell the user to run ` + "`/springfield:plan`" + ` first.

## Step 2 — Execute

Run ` + "`springfield start`" + ` to resume execution from the saved progress.

- Execution is serial by default.
- Parallel execution only happens when the batch explicitly marks independent phases.
- If a slice fails, report the blocker clearly and do not proceed to the next slice.

## Step 3 — Report

After execution, report whether the batch completed or failed, the last blocking slice if any, and what the user should do next.

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
		TaskBody: strings.TrimSpace(`
Inspect the current Springfield batch for the project and report the current state.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

Run ` + "`springfield status`" + ` to get the current Springfield batch state, then summarize:
- The active batch id and title
- The current phase
- Which slices are done, running, blocked, or queued
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
		TaskBody: strings.TrimSpace(`
Recover a Springfield batch that is stalled, blocked, or has a failed slice.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

## Step 1 — Read current state

Run ` + "`springfield status`" + ` to see the active batch, current phase, and slice statuses.

Also read ` + "`.springfield/run.json`" + ` for the last checkpoint and last known error.

## Step 2 — Diagnose

Identify which slice failed or stalled and why. Check:
- The last error in ` + "`run.json`" + `
- Any blockers mentioned in the batch source

## Step 3 — Recover

Propose the safest concrete next step:
- For a failed slice: fix the underlying issue, then run ` + "`springfield start`" + ` to resume from cursor.
- For a blocked slice: explain what needs to happen before execution can continue.
- For a corrupt batch: use ` + "`springfield plan --replace`" + ` to start fresh with a new batch.

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

func lookupLocalTarget(name string) (LocalTarget, error) {
	for _, host := range localTargets {
		if host.Name == name {
			return host, nil
		}
	}
	return LocalTarget{}, fmt.Errorf("unknown Springfield install target %q", name)
}
