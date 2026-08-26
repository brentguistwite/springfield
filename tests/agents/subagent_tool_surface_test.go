package agents_test

import (
	"encoding/json"
	"os/exec"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
)

// settingsDeniedTools parses permissions.deny from the --settings JSON in cmd
// args. permissions.deny is Claude Code's only settings.json mechanism that
// actually removes a tool from the subagent (permissions.allow merely
// pre-approves, it does not restrict); see adapter.go for the rationale.
func settingsDeniedTools(t *testing.T, args []string) []string {
	t.Helper()
	jsonVal := extractSettingsJSON(t, args)
	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonVal), &raw); err != nil {
		t.Fatalf("parse --settings JSON %q: %v", jsonVal, err)
	}
	permsRaw, ok := raw["permissions"]
	if !ok {
		t.Fatalf("expected permissions key in --settings JSON, got %v", raw)
	}
	perms, ok := permsRaw.(map[string]any)
	if !ok {
		t.Fatalf("permissions is not a map: %T", permsRaw)
	}
	denyRaw, ok := perms["deny"]
	if !ok {
		t.Fatalf("expected permissions.deny in --settings JSON, got %v", perms)
	}
	denyList, ok := denyRaw.([]any)
	if !ok {
		t.Fatalf("permissions.deny is not a list: %T", denyRaw)
	}
	out := make([]string, 0, len(denyList))
	for _, v := range denyList {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("permissions.deny entry is not a string: %T", v)
		}
		out = append(out, s)
	}
	return out
}

// TestClaudeSubagentDeniesParentHarnessPrimitives verifies the adapter's
// --settings payload denies every parent-harness primitive the subagent would
// otherwise inherit (Task family, scheduling, web, cron, team, plan/worktree
// mode switches). These are no-op footguns inside a Springfield-managed
// subagent — the plan-04 agent actually invoked ScheduleWakeup from within one.
func TestClaudeSubagentDeniesParentHarnessPrimitives(t *testing.T) {
	a := claude.New(exec.LookPath)
	cmd, err := a.Command(agents.CommandInput{Prompt: "do work", WorkDir: "/tmp/project"})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}

	denied := settingsDeniedTools(t, cmd.Args)
	deniedSet := make(map[string]bool, len(denied))
	for _, d := range denied {
		deniedSet[d] = true
	}

	// Must cover EVERY entry in subagentDeniedTools(). A drop here is a silent
	// regression — if a future refactor removes "ExitPlanMode" from the deny
	// list, the subagent could exit plan-mode in the parent harness session.
	mustDeny := []string{
		// Subagent spawning / orchestration.
		"Task",
		"TaskCreate", "TaskGet", "TaskList", "TaskOutput", "TaskStop", "TaskUpdate",
		"Workflow",
		// Scheduling / background wakeups.
		"ScheduleWakeup",
		"CronCreate", "CronDelete", "CronList",
		"Monitor",
		// Network reach.
		"WebFetch", "WebSearch",
		// Outbound notification / remote-trigger / messaging.
		"PushNotification",
		"RemoteTrigger",
		"SendMessage",
		// Team management.
		"TeamCreate", "TeamDelete",
		// Plan / worktree mode switches owned by the parent harness.
		"EnterPlanMode", "ExitPlanMode",
		"EnterWorktree", "ExitWorktree",
	}
	for _, tool := range mustDeny {
		if !deniedSet[tool] {
			t.Errorf("parent-harness primitive %q must be denied, deny list = %v", tool, denied)
		}
	}
}

// TestClaudeSubagentKeepsImplementerToolsUsable verifies the implementer tool
// surface (read/write/search/shell/skill) is NOT denied, so a subagent can
// still do real work.
func TestClaudeSubagentKeepsImplementerToolsUsable(t *testing.T) {
	a := claude.New(exec.LookPath)
	cmd, err := a.Command(agents.CommandInput{Prompt: "do work", WorkDir: "/tmp/project"})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}

	denied := settingsDeniedTools(t, cmd.Args)
	deniedSet := make(map[string]bool, len(denied))
	for _, d := range denied {
		deniedSet[d] = true
	}

	allowlist := []string{
		"Bash", "Edit", "Glob", "Grep", "MultiEdit",
		"NotebookEdit", "Read", "Skill", "ToolSearch", "Write",
	}
	for _, tool := range allowlist {
		if deniedSet[tool] {
			t.Errorf("implementer tool %q must NOT be denied, deny list = %v", tool, denied)
		}
	}
}

// TestClaudeSubagentDenyListLeavesMcpToolsUntouched verifies the deny list
// names no MCP tools (mcp__ prefix). A deny list that enumerates only built-in
// parent-harness primitives leaves project-configured MCP tools to pass
// through unchanged — the reason a deny list is correct here and a positive
// allowlist (which would have to enumerate unknowable MCP tool names) is not.
func TestClaudeSubagentDenyListLeavesMcpToolsUntouched(t *testing.T) {
	a := claude.New(exec.LookPath)
	cmd, err := a.Command(agents.CommandInput{Prompt: "do work", WorkDir: "/tmp/project"})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}

	for _, tool := range settingsDeniedTools(t, cmd.Args) {
		if len(tool) >= 5 && tool[:5] == "mcp__" {
			t.Errorf("deny list must not name MCP tools (breaks project passthrough), got %q", tool)
		}
	}
}
