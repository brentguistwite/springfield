package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Install renders and writes selected Springfield direct skills into targetDir.
// When names is empty, the full fixed catalog is installed.
func Install(projectRoot, targetDir string, names []string) ([]Installed, error) {
	if strings.TrimSpace(targetDir) == "" {
		return nil, fmt.Errorf("--dir is required")
	}

	selected := names
	if len(selected) == 0 {
		selected = make([]string, 0, len(catalog))
		for _, skill := range catalog {
			selected = append(selected, skill.Name)
		}
	}

	installed := make([]Installed, 0, len(selected))
	for _, name := range selected {
		rendered, err := Render(projectRoot, name)
		if err != nil {
			return nil, err
		}

		skillDir := filepath.Join(targetDir, rendered.Skill.Name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			return nil, fmt.Errorf("create skill dir for %s: %w", rendered.Skill.Name, err)
		}

		path := filepath.Join(skillDir, "SKILL.md")
		if err := os.WriteFile(path, []byte(rendered.Content), 0o644); err != nil {
			return nil, fmt.Errorf("write skill wrapper %s: %w", rendered.Skill.Name, err)
		}

		installed = append(installed, Installed{
			Skill: rendered.Skill,
			Path:  path,
		})
	}

	return installed, nil
}
