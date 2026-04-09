package playbooks

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed builtin/*.md
var builtinFS embed.FS

// Build resolves a Springfield playbook prompt from built-ins, project context, and task text.
func Build(input Input) (Output, error) {
	builtinSource, builtinBody, err := loadBuiltin(input.Kind)
	if err != nil {
		return Output{}, err
	}

	projectSource, projectBody, err := loadProjectContext(input.ProjectRoot)
	if err != nil {
		return Output{}, err
	}

	return Output{
		BuiltinSource: builtinSource,
		ProjectSource: projectSource,
		Prompt: renderPrompt(
			builtinSource,
			builtinBody,
			projectSource,
			projectBody,
			input.TaskBody,
		),
	}, nil
}

func loadBuiltin(kind Kind) (string, string, error) {
	source := "builtin/" + string(kind) + ".md"
	switch kind {
	case KindRalph, KindConductor:
	default:
		return "", "", fmt.Errorf("unsupported playbook kind %q", kind)
	}

	data, err := builtinFS.ReadFile(source)
	if err != nil {
		return "", "", fmt.Errorf("read builtin playbook %s: %w", source, err)
	}

	return source, string(data), nil
}

func renderPrompt(builtinSource, builtinBody, projectSource, projectBody, taskBody string) string {
	sections := []string{
		"# Springfield Playbook\nSource: " + builtinSource + "\n\n" + strings.TrimSpace(builtinBody),
	}

	if strings.TrimSpace(projectBody) != "" {
		sections = append(sections, "# Project Context\nSource: "+projectSource+"\n\n"+strings.TrimSpace(projectBody))
	}

	sections = append(sections, "# Current Task\n\n"+strings.TrimSpace(taskBody))
	return strings.Join(sections, "\n\n")
}
