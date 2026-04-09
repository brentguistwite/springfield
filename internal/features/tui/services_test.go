package tui

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/config"
	"springfield/internal/features/conductor"
	"springfield/internal/features/planner"
	"springfield/internal/features/ralph"
	"springfield/internal/features/workflow"
	"springfield/internal/storage"
)

type fakePlanningSession struct {
	inputs    []string
	responses []planner.Response
	err       error
}

func (f *fakePlanningSession) Next(input string) (planner.Response, error) {
	f.inputs = append(f.inputs, input)
	if f.err != nil {
		return planner.Response{}, f.err
	}
	if len(f.responses) == 0 {
		return planner.Response{}, fmt.Errorf("unexpected planner call for %q", input)
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func TestPlanWorkUsesPlannerBoundary(t *testing.T) {
	root := t.TempDir()
	session := &fakePlanningSession{
		responses: []planner.Response{
			{
				Mode:    planner.ModeDraft,
				WorkID:  "wave-c1",
				Title:   "Wave C1 planning loop",
				Summary: "Connect the TUI planning flow to the real planner session.",
				Split:   planner.SplitSingle,
				Workstreams: []planner.Workstream{
					{Name: "01", Title: "Implement Wave C1", Summary: "Keep it in one stream."},
				},
			},
		},
	}

	var gotRoot string
	services := &runtimeServices{
		cwd: func() (string, error) { return root, nil },
		newPlanningSession: func(projectRoot string) planningSession {
			gotRoot = projectRoot
			return session
		},
	}

	result, err := services.PlanWork("Connect the TUI planning flow to the real planner session")
	if err != nil {
		t.Fatalf("PlanWork: %v", err)
	}

	if gotRoot != root {
		t.Fatalf("planner root = %q, want %q", gotRoot, root)
	}
	if got, want := len(session.inputs), 1; got != want {
		t.Fatalf("planner calls = %d, want %d", got, want)
	}
	if got, want := session.inputs[0], "Connect the TUI planning flow to the real planner session"; got != want {
		t.Fatalf("planner input = %q, want %q", got, want)
	}
	if result.Draft == nil || result.Draft.Title != "Wave C1 planning loop" {
		t.Fatalf("expected reviewable draft from planner boundary, got %#v", result)
	}
}

func TestPlanWorkReturnsPlannerQuestion(t *testing.T) {
	services := &runtimeServices{
		cwd: func() (string, error) { return t.TempDir(), nil },
		newPlanningSession: func(projectRoot string) planningSession {
			return &fakePlanningSession{
				responses: []planner.Response{
					{
						Mode:     planner.ModeQuestion,
						Question: "Which Springfield surface should ship first?",
					},
				},
			}
		},
	}

	result, err := services.PlanWork("Make planning real")
	if err != nil {
		t.Fatalf("PlanWork: %v", err)
	}

	if got, want := result.Question, "Which Springfield surface should ship first?"; got != want {
		t.Fatalf("question = %q, want %q", got, want)
	}
	if result.Draft != nil {
		t.Fatalf("expected no draft when planner asks a question, got %#v", result.Draft)
	}
}

func TestPlanWorkReturnsReviewableDraft(t *testing.T) {
	services := &runtimeServices{
		cwd: func() (string, error) { return t.TempDir(), nil },
		newPlanningSession: func(projectRoot string) planningSession {
			return &fakePlanningSession{
				responses: []planner.Response{
					{
						Mode:    planner.ModeDraft,
						WorkID:  "wave-c1",
						Title:   "Wave C1 planning loop",
						Summary: "Connect the TUI planning flow to the real planner session.",
						Split:   planner.SplitMulti,
						Workstreams: []planner.Workstream{
							{Name: "01", Title: "Planner boundary"},
							{Name: "02", Title: "TUI review flow", Summary: "Wire review and approve."},
						},
					},
				},
			}
		},
	}

	result, err := services.PlanWork("Connect the planner")
	if err != nil {
		t.Fatalf("PlanWork: %v", err)
	}

	if result.Question != "" {
		t.Fatalf("expected draft result, got question %q", result.Question)
	}
	if result.Draft == nil {
		t.Fatal("expected draft result")
	}
	if got, want := result.Draft.WorkID, "wave-c1"; got != want {
		t.Fatalf("work id = %q, want %q", got, want)
	}
	if got, want := result.Draft.Split, planner.SplitMulti; got != want {
		t.Fatalf("split = %q, want %q", got, want)
	}
	if got, want := len(result.Draft.Workstreams), 2; got != want {
		t.Fatalf("workstreams = %d, want %d", got, want)
	}
	if got, want := result.Draft.Workstreams[1].Summary, "Wire review and approve."; got != want {
		t.Fatalf("second summary = %q, want %q", got, want)
	}
}

func TestApprovePlannedWorkWritesWorkflowDraft(t *testing.T) {
	root := t.TempDir()
	services := &runtimeServices{
		cwd: func() (string, error) { return root, nil },
		newPlanningSession: func(projectRoot string) planningSession {
			return &fakePlanningSession{
				responses: []planner.Response{
					{
						Mode:    planner.ModeDraft,
						WorkID:  "wave-c1",
						Title:   "Wave C1 planning loop",
						Summary: "Connect the TUI planning flow to the real planner session.",
						Split:   planner.SplitSingle,
						Workstreams: []planner.Workstream{
							{Name: "01", Title: "Implement Wave C1", Summary: "Keep it in one stream."},
						},
					},
				},
			}
		},
	}

	if _, err := services.PlanWork("Connect the TUI planning flow to the real planner session"); err != nil {
		t.Fatalf("PlanWork: %v", err)
	}
	if err := services.ApprovePlannedWork(); err != nil {
		t.Fatalf("ApprovePlannedWork: %v", err)
	}

	requestPath := filepath.Join(root, ".springfield", "work", "wave-c1", "request.md")
	body, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("read request.md: %v", err)
	}
	if got, want := string(body), "Connect the TUI planning flow to the real planner session"; got != want {
		t.Fatalf("request body = %q, want %q", got, want)
	}

	for _, path := range []string{
		filepath.Join(root, ".springfield", "work", "wave-c1", "workstream-01.json"),
		filepath.Join(root, ".springfield", "work", "wave-c1", "run-state.json"),
		filepath.Join(root, ".springfield", "work", "index.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
	}
}

func TestSpringfieldStatusUsesWorkflowBoundary(t *testing.T) {
	root := t.TempDir()
	writeRuntimeServiceConfig(t, root, strings.Join([]string{
		"[project]",
		`default_agent = "claude"`,
		"",
	}, "\n"))
	writeWorkflowDraftForService(t, root)

	services := runtimeServices{
		cwd:      func() (string, error) { return root, nil },
		lookPath: osexec.LookPath,
	}

	status := services.SpringfieldStatus()
	if !status.Ready {
		t.Fatalf("expected Springfield status ready, got %#v", status)
	}
	if got, want := status.WorkID, "wave-c2"; got != want {
		t.Fatalf("work id = %q, want %q", got, want)
	}
	if got, want := status.Status, "ready"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
	if got, want := len(status.Workstreams), 1; got != want {
		t.Fatalf("workstreams = %d, want %d", got, want)
	}
}

func TestSpringfieldRunUsesProjectExecutionSettings(t *testing.T) {
	root := t.TempDir()
	writeRuntimeServiceConfig(t, root, strings.Join([]string{
		"[project]",
		`default_agent = "claude"`,
		"",
		"[agents.claude]",
		`permission_mode = " bypassPermissions "`,
		"",
	}, "\n"))
	writeWorkflowDraftForService(t, root)

	fakeBinDir := filepath.Join(root, "bin")
	argvPath := filepath.Join(root, "claude.argv")
	installRuntimeServiceFakeBinary(t, fakeBinDir, "claude", argvPath)
	t.Setenv("PATH", fakeBinDir)

	services := runtimeServices{
		cwd:      func() (string, error) { return root, nil },
		lookPath: osexec.LookPath,
	}

	result, err := services.RunSpringfieldWork(nil)
	if err != nil {
		t.Fatalf("RunSpringfieldWork: %v", err)
	}
	if got, want := result.Status, "completed"; got != want {
		t.Fatalf("run status = %q, want %q", got, want)
	}

	args := readRuntimeServiceArgs(t, argvPath)
	for _, want := range []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "bypassPermissions"} {
		if !containsRuntimeServiceArg(args, want) {
			t.Fatalf("expected recorded args to contain %q, got %v", want, args)
		}
	}
}

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

func writeWorkflowDraftForService(t *testing.T, root string) {
	t.Helper()

	if err := workflow.WriteDraft(root, workflow.Draft{
		RequestBody: "Implement Wave C2.",
		Response: planner.Response{
			Mode:    planner.ModeDraft,
			WorkID:  "wave-c2",
			Title:   "Unified execution surface",
			Summary: "Route approved Springfield work through one execution runner.",
			Split:   planner.SplitSingle,
			Workstreams: []planner.Workstream{
				{Name: "01", Title: "Execution adapter", Summary: "Use the unified runner."},
			},
		},
	}); err != nil {
		t.Fatalf("WriteDraft: %v", err)
	}
}

func TestEnsureRecommendedExecutionDefaultsWritesRecommendedWhenUnset(t *testing.T) {
	root := t.TempDir()
	writeRuntimeServiceConfig(t, root, strings.Join([]string{
		"[project]",
		`default_agent = "claude"`,
		"",
	}, "\n"))

	services := runtimeServices{
		cwd: func() (string, error) { return root, nil },
	}

	if err := services.EnsureRecommendedExecutionDefaults(); err != nil {
		t.Fatalf("EnsureRecommendedExecutionDefaults: %v", err)
	}

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}

	if got := loaded.Config.ExecutionModes().Claude; got != config.ExecutionModeRecommended {
		t.Fatalf("claude mode: want %q, got %q", config.ExecutionModeRecommended, got)
	}
	if got := loaded.Config.ExecutionModes().Codex; got != config.ExecutionModeRecommended {
		t.Fatalf("codex mode: want %q, got %q", config.ExecutionModeRecommended, got)
	}
}

func TestEnsureRecommendedExecutionDefaultsPreservesExistingCustomValues(t *testing.T) {
	root := t.TempDir()
	writeRuntimeServiceConfig(t, root, strings.Join([]string{
		"[project]",
		`default_agent = "claude"`,
		"",
		"[agents.claude]",
		`permission_mode = "plan"`,
		"",
		"[agents.codex]",
		`sandbox_mode = "workspace-write"`,
		`approval_policy = "on-request"`,
		"",
	}, "\n"))

	services := runtimeServices{
		cwd: func() (string, error) { return root, nil },
	}

	if err := services.EnsureRecommendedExecutionDefaults(); err != nil {
		t.Fatalf("EnsureRecommendedExecutionDefaults: %v", err)
	}

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}

	if got := loaded.Config.Agents.Claude.PermissionMode; got != "plan" {
		t.Fatalf("claude permission_mode: want plan, got %q", got)
	}
	if got := loaded.Config.Agents.Codex.SandboxMode; got != "workspace-write" {
		t.Fatalf("codex sandbox_mode: want workspace-write, got %q", got)
	}
	if got := loaded.Config.Agents.Codex.ApprovalPolicy; got != "on-request" {
		t.Fatalf("codex approval_policy: want on-request, got %q", got)
	}
}

func TestEnsureRecommendedExecutionDefaultsPreservesExplicitOffValues(t *testing.T) {
	root := t.TempDir()
	writeRuntimeServiceConfig(t, root, strings.Join([]string{
		"[project]",
		`default_agent = "claude"`,
		"",
		"[agents.claude]",
		`permission_mode = ""`,
		"",
		"[agents.codex]",
		`sandbox_mode = ""`,
		`approval_policy = ""`,
		"",
	}, "\n"))

	services := runtimeServices{
		cwd: func() (string, error) { return root, nil },
	}

	if err := services.EnsureRecommendedExecutionDefaults(); err != nil {
		t.Fatalf("EnsureRecommendedExecutionDefaults: %v", err)
	}

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}

	if got := loaded.Config.ExecutionModes().Claude; got != config.ExecutionModeOff {
		t.Fatalf("claude mode: want %q, got %q", config.ExecutionModeOff, got)
	}
	if got := loaded.Config.ExecutionModes().Codex; got != config.ExecutionModeOff {
		t.Fatalf("codex mode: want %q, got %q", config.ExecutionModeOff, got)
	}
}

func TestSaveAgentExecutionModesWritesRecommendedValues(t *testing.T) {
	root := t.TempDir()
	writeRuntimeServiceConfig(t, root, strings.Join([]string{
		"[project]",
		`default_agent = "claude"`,
		"",
	}, "\n"))

	services := runtimeServices{
		cwd: func() (string, error) { return root, nil },
	}

	if err := services.SaveAgentExecutionModes(SaveAgentExecutionModesInput{
		Claude: "recommended",
		Codex:  "recommended",
	}); err != nil {
		t.Fatalf("SaveAgentExecutionModes: %v", err)
	}

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}

	if got := loaded.Config.Agents.Claude.PermissionMode; got != "bypassPermissions" {
		t.Fatalf("claude permission_mode: want bypassPermissions, got %q", got)
	}
	if got := loaded.Config.Agents.Codex.SandboxMode; got != "danger-full-access" {
		t.Fatalf("codex sandbox_mode: want danger-full-access, got %q", got)
	}
	if got := loaded.Config.Agents.Codex.ApprovalPolicy; got != "never" {
		t.Fatalf("codex approval_policy: want never, got %q", got)
	}
}

func TestSaveAgentExecutionModesClearsOffValues(t *testing.T) {
	root := t.TempDir()
	writeRuntimeServiceConfig(t, root, strings.Join([]string{
		"[project]",
		`default_agent = "claude"`,
		"",
		"[agents.claude]",
		`permission_mode = "bypassPermissions"`,
		"",
		"[agents.codex]",
		`sandbox_mode = "danger-full-access"`,
		`approval_policy = "never"`,
		"",
	}, "\n"))

	services := runtimeServices{
		cwd: func() (string, error) { return root, nil },
	}

	if err := services.SaveAgentExecutionModes(SaveAgentExecutionModesInput{
		Claude: "off",
		Codex:  "off",
	}); err != nil {
		t.Fatalf("SaveAgentExecutionModes: %v", err)
	}

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}

	if got := loaded.Config.Agents.Claude.PermissionMode; got != "" {
		t.Fatalf("claude permission_mode: want empty, got %q", got)
	}
	if got := loaded.Config.Agents.Codex.SandboxMode; got != "" {
		t.Fatalf("codex sandbox_mode: want empty, got %q", got)
	}
	if got := loaded.Config.Agents.Codex.ApprovalPolicy; got != "" {
		t.Fatalf("codex approval_policy: want empty, got %q", got)
	}
}

func TestSaveAgentExecutionModesPreservesCustomValues(t *testing.T) {
	root := t.TempDir()
	writeRuntimeServiceConfig(t, root, strings.Join([]string{
		"[project]",
		`default_agent = "claude"`,
		"",
		"[agents.claude]",
		`permission_mode = "plan"`,
		"",
		"[agents.codex]",
		`sandbox_mode = "workspace-write"`,
		`approval_policy = "on-request"`,
		"",
	}, "\n"))

	services := runtimeServices{
		cwd: func() (string, error) { return root, nil },
	}

	if err := services.SaveAgentExecutionModes(SaveAgentExecutionModesInput{
		Claude: "custom",
		Codex:  "custom",
	}); err != nil {
		t.Fatalf("SaveAgentExecutionModes: %v", err)
	}

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}

	if got := loaded.Config.Agents.Claude.PermissionMode; got != "plan" {
		t.Fatalf("claude permission_mode: want plan, got %q", got)
	}
	if got := loaded.Config.Agents.Codex.SandboxMode; got != "workspace-write" {
		t.Fatalf("codex sandbox_mode: want workspace-write, got %q", got)
	}
	if got := loaded.Config.Agents.Codex.ApprovalPolicy; got != "on-request" {
		t.Fatalf("codex approval_policy: want on-request, got %q", got)
	}
}
