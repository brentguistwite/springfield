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
		Summary:      "Start new Springfield work from the current project context.",
		Purpose:      playbooks.PurposeStart,
		Header:       "Springfield Start",
		Description:  "Use Springfield start to shape a new work definition for the current project.",
		RelativePath: "skills/start/SKILL.md",
		TaskBody: strings.TrimSpace(`
Start Springfield work for the current project.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.
Clarify the requested outcome only when the request is underspecified.
Turn the request into a concrete Springfield work definition with named workstreams, constraints, and success criteria.
Keep Springfield as the only user-facing surface.
`),
	},
	{
		Name:         SkillStatus,
		Summary:      "Report the current Springfield work state for the project.",
		Purpose:      playbooks.PurposeStatus,
		Header:       "Springfield Status",
		Description:  "Use Springfield status to inspect current Springfield work and explain where it stands.",
		RelativePath: "skills/status/SKILL.md",
		TaskBody: strings.TrimSpace(`
Inspect the current Springfield work for the project and report the current state.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.
Summarize the active or most recent Springfield work, workstream status, blockers, risks, and the clearest next action.
Do not invent new work unless the user explicitly asks to re-plan it.
Keep Springfield as the only user-facing surface.
`),
	},
	{
		Name:         SkillRecover,
		Summary:      "Recover Springfield work that stalled, failed, or lost state.",
		Purpose:      playbooks.PurposeRecover,
		Header:       "Springfield Recover",
		Description:  "Use Springfield recover to diagnose stuck Springfield work and restore a safe next step.",
		RelativePath: "skills/recover/SKILL.md",
		TaskBody: strings.TrimSpace(`
Recover Springfield work that is stalled, failed, or missing expected state.

Read project guidance from AGENTS.md first, then CLAUDE.md, then GEMINI.md when present.
Identify the break in state, explain what is recoverable, and drive toward the safest concrete next step to resume progress.
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
