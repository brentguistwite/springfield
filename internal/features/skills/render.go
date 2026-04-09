package skills

import (
	"fmt"
	"strings"

	"springfield/internal/features/playbooks"
)

// Render resolves one Springfield direct skill from the shared playbook builder.
func Render(projectRoot, name string) (Rendered, error) {
	skill, err := Lookup(name)
	if err != nil {
		return Rendered{}, err
	}

	output, err := playbooks.Build(playbooks.Input{
		Purpose:     skill.Purpose,
		ProjectRoot: projectRoot,
		TaskBody:    skill.TaskBody,
	})
	if err != nil {
		return Rendered{}, err
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s\n\n", skill.Header)
	builder.WriteString(skill.Description)
	builder.WriteString("\n\n")
	builder.WriteString(strings.TrimSpace(output.Prompt))
	builder.WriteString("\n")

	return Rendered{
		Skill:   skill,
		Prompt:  output.Prompt,
		Content: builder.String(),
	}, nil
}
