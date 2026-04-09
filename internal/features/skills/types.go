package skills

import (
	"fmt"
	"strings"

	"springfield/internal/features/playbooks"
)

// Skill describes one optional Springfield-owned direct skill wrapper.
type Skill struct {
	Name        string
	Summary     string
	Kind        playbooks.Kind
	TaskBody    string
	Header      string
	Description string
}

// Rendered is the resolved prompt plus wrapper content for one direct skill.
type Rendered struct {
	Skill   Skill
	Prompt  string
	Content string
}

// Installed describes one written skill wrapper file.
type Installed struct {
	Skill Skill
	Path  string
}

var catalog = []Skill{
	{
		Name:        "plan",
		Summary:     "Optional Springfield planning wrapper for power users.",
		Kind:        playbooks.KindConductor,
		Header:      "Springfield Direct Skill: plan",
		Description: "Optional Springfield direct skill wrapper. Springfield remains the primary product surface.",
		TaskBody: strings.TrimSpace(`
Plan the user's request for Springfield.

Keep Springfield as the only user-facing surface.
Ask clarifying questions when needed, then drive toward a concrete Springfield work definition with named workstreams.
Stay aligned with the shared Springfield playbook and the current project's guidance.
`),
	},
	{
		Name:        "explain",
		Summary:     "Optional Springfield explanation wrapper for power users.",
		Kind:        playbooks.KindConductor,
		Header:      "Springfield Direct Skill: explain",
		Description: "Optional Springfield direct skill wrapper. Springfield remains the primary product surface.",
		TaskBody: strings.TrimSpace(`
Explain how Springfield should approach work in this project.

Keep Springfield as the only user-facing surface.
Explain the current project context and the built-in playbook guidance that will shape planning and execution.
`),
	},
}

// Catalog returns the fixed Springfield direct skill set.
func Catalog() []Skill {
	out := make([]Skill, len(catalog))
	copy(out, catalog)
	return out
}

// Lookup resolves one named direct skill.
func Lookup(name string) (Skill, error) {
	for _, skill := range catalog {
		if skill.Name == name {
			return skill, nil
		}
	}
	return Skill{}, fmt.Errorf("unknown Springfield direct skill %q", name)
}
