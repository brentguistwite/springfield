package gemini

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hookGuardMatchers is the set of Gemini tool names whose tool_input must be
// inspected before they run. Covers every write or shell entry point.
const hookGuardMatchers = "write_file|replace|run_shell_command"

type hookSpec struct {
	Type    string `json:"type"`
	Name    string `json:"name,omitempty"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

type hookGroup struct {
	Matcher string     `json:"matcher"`
	Hooks   []hookSpec `json:"hooks"`
}

type systemSettings struct {
	Hooks  map[string][]hookGroup `json:"hooks"`
	Agents map[string]any         `json:"agents,omitempty"`
	// NOTE: we deliberately do NOT emit a top-level skills.enabled=false.
	// Gemini CLI v0.39 has no documented settings.json schema for a
	// blanket skill-subsystem disable; emitting a speculative key would
	// silently do nothing and mask the failure. Rely on named
	// agents.overrides entries below, which ARE documented in
	// docs/core/subagents.md. Revisit once Gemini publishes the schema.
}

// writeSystemSettings serialises the per-invocation Gemini CLI override file
// used via GEMINI_CLI_SYSTEM_SETTINGS_PATH. Idempotent: identical inputs
// produce identical bytes. Caller owns the file lifecycle.
//
// root is the project workdir; the file lands at
// <root>/.springfield/gemini-system-settings.json.
func writeSystemSettings(root, hookBin string) (string, error) {
	out := systemSettings{
		Hooks: map[string][]hookGroup{
			"BeforeTool": {{
				Matcher: hookGuardMatchers,
				Hooks: []hookSpec{{
					Type:    "command",
					Name:    "springfield-control-plane-guard",
					Command: shellQuote(hookBin) + " hook-guard",
					Timeout: 5000,
				}},
			}},
		},
		Agents: map[string]any{
			// Disable discovered subagent types named after springfield
			// skills. Matches the agents.overrides schema documented in
			// docs/core/subagents.md.
			"overrides": map[string]any{
				"springfield:start":   map[string]any{"enabled": false},
				"springfield:plan":    map[string]any{"enabled": false},
				"springfield:status":  map[string]any{"enabled": false},
				"springfield:recover": map[string]any{"enabled": false},
			},
		},
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal gemini system settings: %w", err)
	}
	path := filepath.Join(root, ".springfield", "gemini-system-settings.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write gemini system settings: %w", err)
	}
	return path, nil
}

// shellQuote wraps s in single quotes, escaping embedded single quotes.
// Duplicated from the Claude adapter intentionally — cross-boundary sharing
// of a one-line helper is worse than duplication.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
