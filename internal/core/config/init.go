package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// InitOptions controls Init behavior.
type InitOptions struct {
	// Reset backs up the existing config and writes a fresh scaffold, replicating
	// the old unconditional behavior. Without Reset, Init merges into the existing
	// config (preserve-by-default).
	Reset bool
}

// InitResult reports what Init created, updated, or backed up.
type InitResult struct {
	// ConfigCreated is true when springfield.toml was newly created (fresh init only).
	ConfigCreated bool
	// ConfigUpdated is true when merge-mode modified an existing config.
	ConfigUpdated bool
	// BackupPath is non-empty when --reset was requested and a pre-existing config existed.
	BackupPath string
	// RuntimeDirCreated is true when .springfield/ was newly created.
	RuntimeDirCreated bool
}

// Init writes springfield.toml and creates .springfield/ (skip-if-exists).
//
// priority[0] becomes default_agent; the full list is written as agent_priority.
//
// Behavior depends on whether springfield.toml already exists and opts.Reset:
//
//   - Fresh init (no existing config): write scaffold atomically. ConfigCreated=true.
//   - Re-init, no Reset (merge mode): load existing config, update project priority and
//     any absent agent defaults, preserve [plans.*], save atomically. ConfigUpdated=true
//     when the file was changed.
//   - Re-init with Reset: copy existing config to a timestamped backup, write fresh
//     scaffold atomically. BackupPath populated.
func Init(dir string, priority []string, opts InitOptions) (InitResult, error) {
	var result InitResult

	configPath := filepath.Join(dir, FileName)

	exists := false
	if _, err := os.Stat(configPath); err == nil {
		exists = true
	} else if !os.IsNotExist(err) {
		return result, err
	}

	if !exists {
		// Fresh init.
		content := buildScaffold(priority)
		if err := writeFileAtomic(configPath, []byte(content), 0644); err != nil {
			return result, fmt.Errorf("write springfield.toml: %w", err)
		}
		result.ConfigCreated = true
	} else if opts.Reset {
		// Back up existing, write fresh scaffold.
		existing, err := os.ReadFile(configPath)
		if err != nil {
			return result, fmt.Errorf("read existing config: %w", err)
		}
		ts := time.Now().UTC().Format("20060102T150405Z")
		backupPath := configPath + ".bak-" + ts
		if err := writeFileAtomic(backupPath, existing, 0644); err != nil {
			return result, fmt.Errorf("write backup: %w", err)
		}
		result.BackupPath = backupPath

		content := buildScaffold(priority)
		if err := writeFileAtomic(configPath, []byte(content), 0644); err != nil {
			return result, fmt.Errorf("write springfield.toml (original preserved at %s): %w", backupPath, err)
		}
	} else {
		// Merge mode: preserve existing config, fill in missing defaults.
		loaded, err := LoadFrom(dir)
		if err != nil {
			return result, fmt.Errorf("load existing config for merge: %w", err)
		}

		changed := false
		rec := RecommendedExecutionSettings()

		// Always update agent priority and default agent from the provided priority.
		if !slices.Equal(loaded.Config.Project.AgentPriority, priority) {
			loaded.Config.Project.AgentPriority = priority
			changed = true
		}
		if len(priority) > 0 && loaded.Config.Project.DefaultAgent != priority[0] {
			loaded.Config.Project.DefaultAgent = priority[0]
			changed = true
		}

		// Fill in missing Claude defaults.
		if !loaded.Config.Agents.Claude.isPresent && loaded.Config.Agents.Claude.PermissionMode == "" {
			loaded.Config.Agents.Claude.PermissionMode = rec.Claude.PermissionMode
			loaded.Config.Agents.Claude.isPresent = true
			changed = true
		}

		// Fill in missing Codex defaults.
		if !loaded.Config.Agents.Codex.isPresent && loaded.Config.Agents.Codex.SandboxMode == "" && loaded.Config.Agents.Codex.ApprovalPolicy == "" {
			loaded.Config.Agents.Codex.SandboxMode = rec.Codex.SandboxMode
			loaded.Config.Agents.Codex.ApprovalPolicy = rec.Codex.ApprovalPolicy
			loaded.Config.Agents.Codex.isPresent = true
			changed = true
		}

		if changed {
			if err := Save(loaded); err != nil {
				return result, fmt.Errorf("save merged config: %w", err)
			}
			result.ConfigUpdated = true
		}
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

