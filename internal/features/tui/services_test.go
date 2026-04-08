package tui

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/conductor"
	"springfield/internal/features/ralph"
	"springfield/internal/storage"
)

func TestRunRalphNextUsesProjectExecutionSettings(t *testing.T) {
	root := t.TempDir()
	writeRuntimeServiceConfig(t, root, strings.Join([]string{
		"[project]",
		`default_agent = "claude"`,
		"",
		"[agents.claude]",
		`permission_mode = " bypassPermissions "`,
		"",
	}, "\n"))

	workspace, err := ralph.OpenRoot(root)
	if err != nil {
		t.Fatalf("open Ralph workspace: %v", err)
	}
	if err := workspace.InitPlan("refresh", ralph.Spec{
		Project: "springfield",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Bootstrap", Description: "implement bootstrap"},
		},
	}); err != nil {
		t.Fatalf("init Ralph plan: %v", err)
	}

	fakeBinDir := filepath.Join(root, "bin")
	argvPath := filepath.Join(root, "claude.argv")
	installRuntimeServiceFakeBinary(t, fakeBinDir, "claude", argvPath)
	t.Setenv("PATH", fakeBinDir)

	services := runtimeServices{
		cwd:      func() (string, error) { return root, nil },
		lookPath: osexec.LookPath,
	}

	result, err := services.RunRalphNext("refresh", nil)
	if err != nil {
		t.Fatalf("RunRalphNext: %v", err)
	}
	if result.Status != "passed" {
		t.Fatalf("expected passed Ralph run, got %#v", result)
	}

	args := readRuntimeServiceArgs(t, argvPath)
	for _, want := range []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "bypassPermissions"} {
		if !containsRuntimeServiceArg(args, want) {
			t.Fatalf("expected recorded args to contain %q, got %v", want, args)
		}
	}
}

func TestRunConductorNextUsesProjectExecutionSettings(t *testing.T) {
	root := t.TempDir()
	writeRuntimeServiceConfig(t, root, strings.Join([]string{
		"[project]",
		`default_agent = "codex"`,
		"",
		"[agents.codex]",
		`sandbox_mode = " workspace-write "`,
		`approval_policy = " on-request "`,
		"",
	}, "\n"))

	rt, err := storage.FromRoot(root)
	if err != nil {
		t.Fatalf("storage.FromRoot: %v", err)
	}
	if err := rt.WriteJSON("conductor/config.json", &conductor.Config{
		PlansDir:   ".conductor/plans",
		Tool:       "codex",
		Sequential: []string{"01-bootstrap"},
	}); err != nil {
		t.Fatalf("write conductor config: %v", err)
	}

	plansDir := filepath.Join(root, ".conductor", "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatalf("mkdir plans dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(plansDir, "01-bootstrap.md"), []byte("implement bootstrap"), 0o644); err != nil {
		t.Fatalf("write plan file: %v", err)
	}

	fakeBinDir := filepath.Join(root, "bin")
	argvPath := filepath.Join(root, "codex.argv")
	installRuntimeServiceFakeBinary(t, fakeBinDir, "codex", argvPath)
	t.Setenv("PATH", fakeBinDir)

	services := runtimeServices{
		cwd:      func() (string, error) { return root, nil },
		lookPath: osexec.LookPath,
	}

	result, err := services.RunConductorNext(nil)
	if err != nil {
		t.Fatalf("RunConductorNext: %v", err)
	}
	if len(result.Ran) != 1 || result.Ran[0] != "01-bootstrap" {
		t.Fatalf("expected conductor to run 01-bootstrap, got %#v", result)
	}

	args := readRuntimeServiceArgs(t, argvPath)
	for _, want := range []string{"exec", "--json", "-s", "workspace-write", "-a", "on-request"} {
		if !containsRuntimeServiceArg(args, want) {
			t.Fatalf("expected recorded args to contain %q, got %v", want, args)
		}
	}
}

func writeRuntimeServiceConfig(t *testing.T, root, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(root, "springfield.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write springfield.toml: %v", err)
	}
}

func installRuntimeServiceFakeBinary(t *testing.T, binDir, name, argvPath string) {
	t.Helper()

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bin dir: %v", err)
	}

	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\necho 'agent-output'\n", argvPath)
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s binary: %v", name, err)
	}
}

func readRuntimeServiceArgs(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recorded args: %v", err)
	}

	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}

	return strings.Split(text, "\n")
}

func containsRuntimeServiceArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
