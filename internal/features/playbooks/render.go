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
	builtinSource, builtinBody, err := loadBuiltin(input.Purpose)
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

func loadBuiltin(purpose Purpose) (string, string, error) {
	source, err := builtinSourceForPurpose(purpose)
	if err != nil {
		return "", "", err
	}

	data, err := builtinFS.ReadFile(source)
	if err != nil {
		return "", "", fmt.Errorf("read builtin playbook %s: %w", source, err)
	}

	return source, string(data), nil
}

func builtinSourceForPurpose(purpose Purpose) (string, error) {
	switch purpose {
	case PurposePlan, PurposeExplain:
		return "builtin/springfield.md", nil
	default:
		return "", fmt.Errorf("unsupported playbook purpose %q", purpose)
	}
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
