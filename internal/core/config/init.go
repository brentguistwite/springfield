package config

import (
	"os"
	"path/filepath"
)

const defaultConfigContent = `[project]
default_agent = "claude"

[agents.claude]
permission_mode = "bypassPermissions"

[agents.codex]
sandbox_mode = "danger-full-access"
approval_policy = "never"
`

// InitResult reports what Init created vs skipped.
type InitResult struct {
	ConfigCreated     bool
	RuntimeDirCreated bool
}

// Init creates springfield.toml and .springfield/ in dir.
// Safe to re-run: skips existing files/dirs without overwriting.
func Init(dir string) (InitResult, error) {
	var result InitResult

	configPath := filepath.Join(dir, FileName)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := os.WriteFile(configPath, []byte(defaultConfigContent), 0644); err != nil {
			return result, err
		}
		result.ConfigCreated = true
	} else if err != nil {
		return result, err
	}

	runtimeDir := filepath.Join(dir, ".springfield")
	if _, err := os.Stat(runtimeDir); os.IsNotExist(err) {
		if err := os.MkdirAll(runtimeDir, 0755); err != nil {
			return result, err
		}
		result.RuntimeDirCreated = true
	} else if err != nil {
		return result, err
	}

	return result, nil
}
