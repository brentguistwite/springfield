package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"springfield/internal/core/agents"
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
// priority is written as agent_priority. The first entry is the implicit default
// agent at runtime; no explicit default_agent field is emitted. An empty priority
// produces `agent_priority = []` and no agent blocks. Init NEVER auto-emits
// [agents.<id>] blocks for agents the caller did not include in priority.
//
// Behavior depends on whether springfield.toml already exists and opts.Reset:
//
//   - Fresh init (no existing config): write scaffold atomically. ConfigCreated=true.
//   - Re-init, no Reset (merge mode): load existing config, update project priority and
//     fill defaults only for agents in priority that are absent, preserve [plans.*],
//     save atomically. ConfigUpdated=true when the file was changed.
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

		// Always update agent priority from the provided priority.
		if !slices.Equal(loaded.Config.Project.AgentPriority, priority) {
			loaded.Config.Project.AgentPriority = priority
			changed = true
		}

		// Merge mode: fill missing defaults *only* for agents in the requested
		// priority. Never auto-add [agents.<id>] blocks for agents the user did
		// not ask for. The field-empty check alongside isPresent is defensive:
		// isPresent comes from the TOML decoder metadata, and the extra
		// empty-field test guards against any future drift where a struct is
		// marked present but fields arrived zeroed.
		if slices.Contains(priority, string(agents.AgentClaude)) &&
			!loaded.Config.Agents.Claude.isPresent &&
			loaded.Config.Agents.Claude.PermissionMode == "" {
			loaded.Config.Agents.Claude.PermissionMode = rec.Claude.PermissionMode
			loaded.Config.Agents.Claude.isPresent = true
			changed = true
		}

		if slices.Contains(priority, string(agents.AgentCodex)) &&
			!loaded.Config.Agents.Codex.isPresent &&
			loaded.Config.Agents.Codex.SandboxMode == "" &&
			loaded.Config.Agents.Codex.ApprovalPolicy == "" {
			loaded.Config.Agents.Codex.SandboxMode = rec.Codex.SandboxMode
			loaded.Config.Agents.Codex.ApprovalPolicy = rec.Codex.ApprovalPolicy
			loaded.Config.Agents.Codex.isPresent = true
			changed = true
		}

		if slices.Contains(priority, string(agents.AgentGemini)) &&
			!loaded.Config.Agents.Gemini.isPresent &&
			loaded.Config.Agents.Gemini.ApprovalMode == "" &&
			loaded.Config.Agents.Gemini.SandboxMode == "" &&
			loaded.Config.Agents.Gemini.Model == "" {
			loaded.Config.Agents.Gemini.ApprovalMode = rec.Gemini.ApprovalMode
			loaded.Config.Agents.Gemini.SandboxMode = rec.Gemini.SandboxMode
			loaded.Config.Agents.Gemini.Model = rec.Gemini.Model
			loaded.Config.Agents.Gemini.isPresent = true
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

// buildScaffold returns the initial TOML content. Only [agents.<id>] blocks for
// agents listed in priority are emitted; an empty priority produces just the
// [project] section with `agent_priority = []` and no agent blocks. The first
// entry of priority is the implicit default at runtime — no default_agent field
// is emitted.
func buildScaffold(priority []string) string {
	// Format agent_priority as a TOML inline array.
	quoted := make([]string, len(priority))
	for i, a := range priority {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	agentPriority := "[" + strings.Join(quoted, ", ") + "]"

	rec := RecommendedExecutionSettings()

	base := fmt.Sprintf("[project]\nagent_priority = %s\n", agentPriority)

	if slices.Contains(priority, string(agents.AgentClaude)) {
		base += fmt.Sprintf("\n[agents.claude]\npermission_mode = %q\n",
			rec.Claude.PermissionMode)
	}
	if slices.Contains(priority, string(agents.AgentCodex)) {
		base += fmt.Sprintf("\n[agents.codex]\nsandbox_mode = %q\napproval_policy = %q\n",
			rec.Codex.SandboxMode, rec.Codex.ApprovalPolicy)
	}
	if slices.Contains(priority, string(agents.AgentGemini)) {
		base += fmt.Sprintf("\n[agents.gemini]\napproval_mode = %q\nsandbox_mode = %q\n",
			rec.Gemini.ApprovalMode, rec.Gemini.SandboxMode)
	}

	return base
}

