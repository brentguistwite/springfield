package skills

import (
	"fmt"
	"strings"

	"springfield/internal/features/playbooks"
)

// Name identifies one Springfield-owned skill contract.
type Name string

const (
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
		Name:         SkillStart,
		Summary:      "Execute the active Springfield batch from its saved cursor.",
		Purpose:      playbooks.PurposeStart,
		Header:       "Springfield Start",
		Description:  "Use Springfield start to execute the active batch for the current project from its saved cursor.",
		RelativePath: "skills/start/SKILL.md",
		TaskBody: strings.TrimSpace(`
Execute the active Springfield batch for the current project.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.

## Step 1 — Check for active batch

Run ` + "`springfield status`" + ` to confirm an active batch exists and review its current cursor.

If no active batch exists, stop and tell the user to run ` + "`/springfield:plan`" + ` first.

## Step 2 — Execute

Run ` + "`springfield start`" + ` to execute from the saved cursor.

- Execution is serial by default.
- Parallel execution only happens when the batch explicitly marks independent phases.
- If a slice fails, report the blocker clearly and do not proceed to the next slice.

## Step 3 — Report

After execution, report the batch outcome: which slices completed, which failed and why, and what the user should do next.

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

Run ` + "`springfield status`" + ` to get the machine-readable view, then summarize:
- The active batch id and title
- The current phase and integration mode
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
- The slice's branch or worktree refs if set
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
