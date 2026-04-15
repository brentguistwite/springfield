package skills

import (
	"fmt"
	"strings"

	"springfield/internal/features/playbooks"
)

// Render resolves one canonical Springfield skill file.
func Render(name string) (Rendered, error) {
	skill, err := Lookup(name)
	if err != nil {
		return Rendered{}, err
	}

	output, err := playbooks.Build(playbooks.Input{
		Purpose:               skill.Purpose,
		ProjectRoot:           "",
		TaskBody:              skill.TaskBody,
		IncludeProjectContext: false,
	})
	if err != nil {
		return Rendered{}, err
	}

	content := renderContent(skill.Header, skill.Description, output.Prompt)
	return Rendered{
		Skill:   skill,
		Prompt:  output.Prompt,
		Content: renderSkillFrontmatter(string(skill.Name), skill.Description) + content,
	}, nil
}

// RenderCommand resolves one canonical Springfield Claude command file.
func RenderCommand(name string) (Rendered, error) {
	skill, err := Lookup(name)
	if err != nil {
		return Rendered{}, err
	}

	output, err := playbooks.Build(playbooks.Input{
		Purpose:               skill.Purpose,
		ProjectRoot:           "",
		TaskBody:              skill.TaskBody,
		IncludeProjectContext: false,
	})
	if err != nil {
		return Rendered{}, err
	}

	content := renderCommandFrontmatter(skill.Description) + renderContent(skill.Header, skill.Description, output.Prompt)
	content += "\n## Invocation Input\n\nUser input from the slash command invocation:\n\n$ARGUMENTS\n\nIf `$ARGUMENTS` is empty, continue with the default Springfield behavior for this command.\n"
	return Rendered{
		Skill:   skill,
		Prompt:  output.Prompt,
		Content: content,
	}, nil
}

func renderLocalTarget(projectRoot string, host LocalTarget) (string, error) {
	output, err := playbooks.Build(playbooks.Input{
		Purpose:               host.Purpose,
		ProjectRoot:           projectRoot,
		TaskBody:              host.TaskBody,
		IncludeProjectContext: true,
	})
	if err != nil {
		return "", err
	}

	content := renderContent(host.Header, host.Description, output.Prompt)
	if host.Name == "codex" {
		content = renderSkillFrontmatter("springfield", host.Description) + content
	}

	var skillNames []string
	for _, skill := range Catalog() {
		skillNames = append(skillNames, string(skill.Name))
	}
	if len(skillNames) == 0 {
		return content, nil
	}

	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(content))
	builder.WriteString("\n\n## Springfield Skills\n\n")
	for _, name := range skillNames {
		builder.WriteString("- ")
		builder.WriteString(name)
		builder.WriteString("\n")
	}
	return builder.String(), nil
}

func renderContent(header, description, prompt string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s\n\n", header)
	builder.WriteString(description)
	builder.WriteString("\n\n")
	builder.WriteString(strings.TrimSpace(prompt))
	builder.WriteString("\n")
	return builder.String()
}

func renderSkillFrontmatter(name, description string) string {
	var builder strings.Builder
	builder.WriteString("---\n")
	fmt.Fprintf(&builder, "name: %s\n", name)
	fmt.Fprintf(&builder, "description: %s\n", description)
	builder.WriteString("---\n\n")
	return builder.String()
}

func renderCommandFrontmatter(description string) string {
	var builder strings.Builder
	builder.WriteString("---\n")
	fmt.Fprintf(&builder, "description: %s\n", description)
	builder.WriteString("---\n\n")
	return builder.String()
}
