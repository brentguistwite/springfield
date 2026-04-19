package agents_test

import (
	"encoding/json"
	"os/exec"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
)

// TestClaudeAdapterInjectsSpringfieldDenySettings verifies the adapter
// appends a --settings JSON blob carrying the Springfield control-plane deny
// rules on every command, regardless of permission_mode.
func TestClaudeAdapterInjectsSpringfieldDenySettings(t *testing.T) {
	adapter := claude.New(exec.LookPath)
	commander := adapter.(agents.Commander)

	cmd := commander.Command(agents.CommandInput{
		Prompt:  "do work",
		WorkDir: "/tmp/project",
	})

	jsonVal := extractSettingsJSON(t, cmd.Args)

	var parsed struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(jsonVal), &parsed); err != nil {
		t.Fatalf("parse --settings JSON %q: %v", jsonVal, err)
	}

	wantDeny := []string{
		"Write(.springfield/**)",
		"Edit(.springfield/**)",
		"Bash(* .springfield/**)",
	}
	if !stringSlicesEqual(parsed.Permissions.Deny, wantDeny) {
		t.Fatalf("deny list = %v, want %v", parsed.Permissions.Deny, wantDeny)
	}
}

// TestClaudeAdapterDenyRulesCoexistWithBypassPermissions verifies deny rules
// are injected even when the user opts into bypassPermissions — Claude CLI
// treats deny as higher-precedence than bypass, so both must be present.
func TestClaudeAdapterDenyRulesCoexistWithBypassPermissions(t *testing.T) {
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

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
