package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Install renders and writes Springfield local host artifacts.
// When opts.Hosts is empty, the full fixed host target set is installed.
func Install(projectRoot string, opts InstallOptions) ([]Installed, error) {
	hosts, err := selectedHosts(opts.Hosts)
	if err != nil {
		return nil, err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home: %w", err)
	}

	claudeDir := strings.TrimSpace(opts.ClaudeDir)
	if claudeDir == "" {
		claudeDir = filepath.Join(home, ".claude", "commands")
	}

	codexDir := strings.TrimSpace(opts.CodexDir)
	if codexDir == "" {
		codexDir = filepath.Join(home, ".agents", "skills")
	}

	installed := make([]Installed, 0, len(hosts))
	for _, host := range hosts {
		content, err := renderLocalTarget(projectRoot, host)
		if err != nil {
			return nil, err
		}

		targetRoot := targetRootDir(host.Name, claudeDir, codexDir)
		path := filepath.Join(targetRoot, host.RelativePath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create parent dir for %s: %w", host.Name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("write %s artifact: %w", host.Name, err)
		}

		installed = append(installed, Installed{
			Host: host,
			Path: path,
		})
	}

	return installed, nil
}

func selectedHosts(names []string) ([]LocalTarget, error) {
	if len(names) == 0 {
		return localCatalog(), nil
	}

	want := make(map[string]bool, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		want[trimmed] = true
	}

	hosts := make([]LocalTarget, 0, len(want))
	for _, host := range localCatalog() {
		if want[host.Name] {
			hosts = append(hosts, host)
			delete(want, host.Name)
		}
	}
	if len(want) > 0 {
		for name := range want {
			return nil, fmt.Errorf("unknown Springfield install target %q", name)
		}
	}

	return hosts, nil
}

func targetRootDir(name, claudeDir, codexDir string) string {
	switch name {
	case "claude-code":
		return claudeDir
	case "codex":
		return codexDir
	default:
		return ""
	}
}
