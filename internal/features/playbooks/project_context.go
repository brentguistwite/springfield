package playbooks

import (
	"fmt"
	"os"
	"path/filepath"
)

func loadProjectContext(root string) (string, string, error) {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if err == nil {
			return path, string(data), nil
		}
		if !os.IsNotExist(err) {
			return "", "", fmt.Errorf("read %s: %w", path, err)
		}
	}

	return "", "", nil
}
