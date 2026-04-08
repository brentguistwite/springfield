package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/ralph"
)

func writeRalphSpec(t *testing.T, dir string, spec ralph.Spec) string {
	t.Helper()

	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		t.Fatalf("marshal Ralph spec: %v", err)
	}

	path := filepath.Join(dir, "ralph-spec.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write Ralph spec: %v", err)
	}

	return path
}

func writeSpringfieldConfig(t *testing.T, dir string, agent string) {
	t.Helper()

	content := "[project]\ndefault_agent = \"" + agent + "\"\n"
	path := filepath.Join(dir, "springfield.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write springfield.toml: %v", err)
	}
}

func TestRalphInitAcceptsPRDFormat(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	writeSpringfieldConfig(t, dir, "claude")

	// Write a PRD-format spec with "userStories", "passes", and "deps".
	prdJSON := `{
		"project": "prd-test",
		"branchName": "prd/test",
		"description": "PRD-format plan",
		"userStories": [
			{"id": "US-001", "title": "First", "passes": false, "deps": []},
			{"id": "US-002", "title": "Second", "passes": true, "deps": ["US-001"]}
		]
	}`
	specPath := filepath.Join(dir, "prd-spec.json")
	if err := os.WriteFile(specPath, []byte(prdJSON), 0o644); err != nil {
		t.Fatalf("write PRD spec: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "ralph", "init", "--name", "prd", "--spec", specPath)
	if err != nil {
		t.Fatalf("ralph init with PRD format failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "with 2 stories") {
		t.Fatalf("expected 2 stories from PRD userStories, got:\n%s", output)
	}

	output, err = runBinaryIn(t, bin, dir, "ralph", "status", "--name", "prd")
	if err != nil {
		t.Fatalf("ralph status failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "US-001  pending") {
		t.Fatalf("expected US-001 pending (passes:false), got:\n%s", output)
	}
	if !strings.Contains(output, "US-002  passed") {
		t.Fatalf("expected US-002 passed (passes:true), got:\n%s", output)
	}
}

func TestRalphInitStatusAndRun(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	writeSpringfieldConfig(t, dir, "claude")

	specPath := writeRalphSpec(t, dir, ralph.Spec{
		Project:     "springfield",
		Description: "refresh prompt",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Bootstrap"},
			{ID: "US-002", Title: "Refresh", DependsOn: []string{"US-001"}},
		},
	})

	output, err := runBinaryIn(t, bin, dir, "ralph", "init", "--name", "refresh", "--spec", specPath)
	if err != nil {
		t.Fatalf("ralph init failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Initialized Ralph plan \"refresh\" with 2 stories.") {
		t.Fatalf("expected init output, got:\n%s", output)
	}

	output, err = runBinaryIn(t, bin, dir, "ralph", "status", "--name", "refresh")
	if err != nil {
		t.Fatalf("ralph status failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Plan: refresh") {
		t.Fatalf("expected plan name in status output, got:\n%s", output)
	}

	if !strings.Contains(output, "US-001  pending") {
		t.Fatalf("expected pending story in status output, got:\n%s", output)
	}

	// Real runner will fail because no agent binary is available in CI.
	// The run should still succeed (record the failure) and report truthfully.
	output, err = runBinaryInWithEnv(
		t,
		bin,
		dir,
		[]string{"PATH=" + t.TempDir()},
		"ralph", "run", "--name", "refresh",
	)
	if err != nil {
		t.Fatalf("ralph run failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Story US-001: failed") {
		t.Fatalf("expected truthful failed status from real runner, got:\n%s", output)
	}

	if !strings.Contains(output, "agent: claude") {
		t.Fatalf("expected agent name in output, got:\n%s", output)
	}

	if !strings.Contains(output, "Error:") {
		t.Fatalf("expected error detail in output, got:\n%s", output)
	}

	// Story should remain unpassed after a failed run.
	output, err = runBinaryIn(t, bin, dir, "ralph", "status", "--name", "refresh")
	if err != nil {
		t.Fatalf("ralph status after run failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "US-001  pending") {
		t.Fatalf("expected story to remain pending after failed run, got:\n%s", output)
	}
}

func TestRalphHelpShowsRealSubcommands(t *testing.T) {
	output, err := runSpringfield(t, "ralph", "--help")
	if err != nil {
		t.Fatalf("springfield ralph --help failed: %v\n%s", err, output)
	}

	for _, marker := range []string{"init", "status", "run", "Manage Ralph plans"} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected Ralph help to mention %q, got:\n%s", marker, output)
		}
	}
}

func TestRalphRunFailsWhenNoEligibleStoriesRemain(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	writeSpringfieldConfig(t, dir, "claude")

	specPath := writeRalphSpec(t, dir, ralph.Spec{
		Project: "springfield",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Bootstrap", Passed: true},
		},
	})

	output, err := runBinaryIn(t, bin, dir, "ralph", "init", "--name", "refresh", "--spec", specPath)
	if err != nil {
		t.Fatalf("ralph init failed: %v\n%s", err, output)
	}

	output, err = runBinaryIn(t, bin, dir, "ralph", "run", "--name", "refresh")
	if err == nil {
		t.Fatalf("expected Ralph run to fail when no stories remain, output:\n%s", output)
	}

	if !strings.Contains(output, "no eligible story") {
		t.Fatalf("expected no eligible story error, got:\n%s", output)
	}
}

func TestRalphRunFailsWithoutConfig(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	specPath := writeRalphSpec(t, dir, ralph.Spec{
		Project: "springfield",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Bootstrap"},
		},
	})

	output, err := runBinaryIn(t, bin, dir, "ralph", "init", "--name", "refresh", "--spec", specPath)
	if err != nil {
		t.Fatalf("ralph init failed: %v\n%s", err, output)
	}

	output, err = runBinaryIn(t, bin, dir, "ralph", "run", "--name", "refresh")
	if err == nil {
		t.Fatalf("expected ralph run to fail without springfield.toml, output:\n%s", output)
	}

	if !strings.Contains(output, "missing") || !strings.Contains(output, "springfield.toml") {
		t.Fatalf("expected missing config error, got:\n%s", output)
	}
}

func TestRalphRunUsesEffectivePriorityHead(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	config := strings.Join([]string{
		"[project]",
		`default_agent = "claude"`,
		`agent_priority = ["gemini", "claude"]`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "springfield.toml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write springfield.toml: %v", err)
	}

	specPath := writeRalphSpec(t, dir, ralph.Spec{
		Project: "springfield",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Bootstrap"},
		},
	})

	output, err := runBinaryIn(t, bin, dir, "ralph", "init", "--name", "refresh", "--spec", specPath)
	if err != nil {
		t.Fatalf("ralph init failed: %v\n%s", err, output)
	}

	output, err = runBinaryInWithEnv(
		t,
		bin,
		dir,
		[]string{"PATH=" + t.TempDir()},
		"ralph", "run", "--name", "refresh",
	)
	if err != nil {
		t.Fatalf("ralph run failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "agent: gemini") {
		t.Fatalf("expected priority head gemini in output, got:\n%s", output)
	}

	if strings.Contains(output, "agent: claude") {
		t.Fatalf("expected run to avoid default agent fallback when priority head is gemini, got:\n%s", output)
	}
}

func TestRalphRunPassesClaudePermissionModeToSubprocess(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	configBody := strings.Join([]string{
		"[project]",
		`default_agent = "claude"`,
		"",
		"[agents.claude]",
		`permission_mode = "bypassPermissions"`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "springfield.toml"), []byte(configBody), 0o644); err != nil {
		t.Fatalf("write springfield.toml: %v", err)
	}

	specPath := writeRalphSpec(t, dir, ralph.Spec{
		Project: "springfield",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Bootstrap", Description: "implement bootstrap"},
		},
	})

	output, err := runBinaryIn(t, bin, dir, "ralph", "init", "--name", "refresh", "--spec", specPath)
	if err != nil {
		t.Fatalf("ralph init failed: %v\n%s", err, output)
	}

	fakeBinDir := filepath.Join(dir, "bin")
	argvPath := filepath.Join(dir, "claude.argv")
	installFakeAgentBinary(t, fakeBinDir, "claude", argvPath)

	output, err = runBinaryInWithEnv(
		t,
		bin,
		dir,
		[]string{"PATH=" + fakeBinDir},
		"ralph", "run", "--name", "refresh",
	)
	if err != nil {
		t.Fatalf("ralph run failed: %v\n%s", err, output)
	}

	args := readRecordedArgs(t, argvPath)
	for _, want := range []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "bypassPermissions"} {
		if !containsArg(args, want) {
			t.Fatalf("expected recorded args to contain %q, got %v", want, args)
		}
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
