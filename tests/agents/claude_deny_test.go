package agents_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
)

// TestClaudeAdapterInjectsControlPlaneHookSettings verifies the adapter
// appends a --settings JSON blob carrying a PreToolUse hook that blocks
// writes to Springfield's control plane (.springfield/). The hook is a
// substring grep that catches bypass forms (absolute paths, cd, redirects)
// that a lexical deny list would miss.
func TestClaudeAdapterInjectsControlPlaneHookSettings(t *testing.T) {
	a := claude.New(exec.LookPath)
	commander := a.(agents.Commander)

	cmd := commander.Command(agents.CommandInput{
		Prompt:  "do work",
		WorkDir: "/tmp/project",
	})

	jsonVal := extractSettingsJSON(t, cmd.Args)

	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonVal), &raw); err != nil {
		t.Fatalf("parse --settings JSON %q: %v", jsonVal, err)
	}

	if _, ok := raw["permissions"]; ok {
		t.Fatalf("expected no permissions key in settings, got %v", raw)
	}

	hooks, ok := raw["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("expected hooks map in settings, got %v", raw)
	}
	preList, ok := hooks["PreToolUse"].([]any)
	if !ok || len(preList) == 0 {
		t.Fatalf("expected PreToolUse list, got %v", hooks)
	}
	first, ok := preList[0].(map[string]any)
	if !ok {
		t.Fatalf("expected PreToolUse[0] to be a map, got %T", preList[0])
	}

	if got, want := first["matcher"], "Write|Edit|MultiEdit|NotebookEdit|Bash"; got != want {
		t.Fatalf("matcher = %v, want %q", got, want)
	}

	inner, ok := first["hooks"].([]any)
	if !ok || len(inner) == 0 {
		t.Fatalf("expected inner hooks list, got %v", first)
	}
	innerFirst, ok := inner[0].(map[string]any)
	if !ok {
		t.Fatalf("expected inner hooks[0] map, got %T", inner[0])
	}
	if got, want := innerFirst["type"], "command"; got != want {
		t.Fatalf("hook type = %v, want %q", got, want)
	}
	cmdStr, ok := innerFirst["command"].(string)
	if !ok {
		t.Fatalf("expected hook command string, got %T", innerFirst["command"])
	}
	if want := a.(interface{ SpringfieldControlPlaneHookCommand() string }).SpringfieldControlPlaneHookCommand(); cmdStr != want {
		t.Fatalf("hook command = %q, want %q", cmdStr, want)
	}
	if !strings.Contains(cmdStr, "hook-guard") {
		t.Fatalf("hook command should invoke hook-guard subcommand, got %q", cmdStr)
	}
	if strings.Contains(cmdStr, "grep") {
		t.Fatalf("hook command should no longer shell out to grep, got %q", cmdStr)
	}
}

// TestClaudeAdapterHookSettingsCoexistWithBypassPermissions verifies the
// hook settings are injected even when the user opts into bypassPermissions.
// The hook blocks tool calls before Claude's permission system sees them, so
// it enforces protection regardless of permission mode.
func TestClaudeAdapterHookSettingsCoexistWithBypassPermissions(t *testing.T) {
	adapter := claude.New(exec.LookPath)
	commander := adapter.(agents.Commander)

	cmd := commander.Command(agents.CommandInput{
		Prompt:  "do work",
		WorkDir: "/tmp/project",
		ExecutionSettings: agents.ExecutionSettings{
			Claude: agents.ClaudeExecutionSettings{PermissionMode: "bypassPermissions"},
		},
	})

	assertArgsContain(t, cmd.Args, "--permission-mode", "bypassPermissions")
	jsonVal := extractSettingsJSON(t, cmd.Args)
	if jsonVal == "" {
		t.Fatalf("expected --settings JSON when bypassPermissions is set, got args=%v", cmd.Args)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonVal), &raw); err != nil {
		t.Fatalf("parse --settings JSON: %v", err)
	}
	if _, ok := raw["hooks"]; !ok {
		t.Fatalf("expected hooks key in settings even with bypassPermissions, got %v", raw)
	}
}

func extractSettingsJSON(t *testing.T, args []string) string {
	t.Helper()
	for i, a := range args {
		if a == "--settings" {
			if i+1 >= len(args) {
				t.Fatalf("--settings flag has no value: %v", args)
			}
			return args[i+1]
		}
	}
	t.Fatalf("expected --settings flag in args, got %v", args)
	return ""
}
