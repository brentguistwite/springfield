package skills

import (
	"fmt"
	"strings"

	"springfield/internal/features/playbooks"
)

// Render resolves one Springfield host artifact from the shared playbook builder.
func Render(projectRoot, name string) (Rendered, error) {
	host, err := Lookup(name)
	if err != nil {
		return Rendered{}, err
	}

	output, err := playbooks.Build(playbooks.Input{
		Purpose:     host.Purpose,
		ProjectRoot: projectRoot,
		TaskBody:    host.TaskBody,
	})
	if err != nil {
		return Rendered{}, err
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s\n\n", host.Header)
	builder.WriteString(host.Description)
	builder.WriteString("\n\n")
	builder.WriteString(strings.TrimSpace(output.Prompt))
	builder.WriteString("\n")

	return Rendered{
		Host:    host,
		Prompt:  output.Prompt,
		Content: builder.String(),
	}, nil
}
