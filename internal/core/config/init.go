package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// InitResult reports what Init created or backed up.
type InitResult struct {
	// BackupPath is non-empty when a pre-existing springfield.toml was renamed.
	BackupPath string
	// RuntimeDirCreated is true when .springfield/ was newly created.
	RuntimeDirCreated bool
}

// Init writes springfield.toml (always) and creates .springfield/ (skip-if-exists).
//
// priority[0] becomes default_agent; the full list is written as agent_priority.
// Both [agents.claude] and [agents.codex] sections are always emitted with
// recommended defaults so users don't have to hand-edit them later.
//
// If springfield.toml already exists it is renamed to
// springfield.toml.bak-<UTC ISO8601 compact> and the path is returned in
// InitResult.BackupPath.
func Init(dir string, priority []string) (InitResult, error) {
	var result InitResult

	configPath := filepath.Join(dir, FileName)

	// Backup pre-existing config.
	if _, err := os.Stat(configPath); err == nil {
		ts := time.Now().UTC().Format("20060102T150405Z")
		backupPath := configPath + ".bak-" + ts
		if err := os.Rename(configPath, backupPath); err != nil {
			return result, fmt.Errorf("backup existing config: %w", err)
		}
		result.BackupPath = backupPath
	} else if !os.IsNotExist(err) {
		return result, err
	}

	content := buildScaffold(priority)
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return result, fmt.Errorf("write springfield.toml: %w", err)
	}

	runtimeDir := filepath.Join(dir, ".springfield")
	if _, err := os.Stat(runtimeDir); os.IsNotExist(err) {
		if err := os.MkdirAll(runtimeDir, 0755); err != nil {
			return result, fmt.Errorf("create runtime dir: %w", err)
		}
		result.RuntimeDirCreated = true
	} else if err != nil {
		return result, fmt.Errorf("stat runtime dir: %w", err)
	}

	return result, nil
}

// buildScaffold returns the initial TOML content. Both agent sections are always
// emitted so users have recommended defaults ready to edit without hand-crafting them.
func buildScaffold(priority []string) string {
	defaultAgent := ""
	if len(priority) > 0 {
		defaultAgent = priority[0]
	}

	// Format agent_priority as a TOML inline array.
	quoted := make([]string, len(priority))
	for i, a := range priority {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	agentPriority := "[" + strings.Join(quoted, ", ") + "]"

	rec := RecommendedExecutionSettings()

	return fmt.Sprintf(`[project]
default_agent = %q
agent_priority = %s

[agents.claude]
permission_mode = %q

[agents.codex]
sandbox_mode = %q
approval_policy = %q
`, defaultAgent, agentPriority, rec.Claude.PermissionMode, rec.Codex.SandboxMode, rec.Codex.ApprovalPolicy)
}
