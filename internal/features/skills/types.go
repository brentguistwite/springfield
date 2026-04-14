package skills

import (
	"fmt"
	"strings"

	"springfield/internal/features/playbooks"
)

// Host describes one Springfield-owned installation target.
type Host struct {
	Name         string
	Summary      string
	Purpose      playbooks.Purpose
	TaskBody     string
	Header       string
	Description  string
	RelativePath string
}

// Rendered is the resolved prompt plus host-specific artifact content.
type Rendered struct {
	Host    Host
	Prompt  string
	Content string
}

// Installed describes one written host artifact file.
type Installed struct {
	Host Host
	Path string
}

// InstallOptions controls where Springfield writes host artifacts.
type InstallOptions struct {
	Hosts     []string
	ClaudeDir string
	CodexDir  string
}

var catalog = []Host{
	{
		Name:         "claude-code",
		Summary:      "Springfield slash command for Claude Code.",
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
		Summary:      "Springfield skill for Codex.",
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

// Catalog returns the fixed Springfield host target set.
func Catalog() []Host {
	out := make([]Host, len(catalog))
	copy(out, catalog)
	return out
}

// Lookup resolves one named host target.
func Lookup(name string) (Host, error) {
	for _, host := range catalog {
		if host.Name == name {
			return host, nil
		}
	}
	return Host{}, fmt.Errorf("unknown Springfield install target %q", name)
}
