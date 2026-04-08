package ralph_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/exec"
	"springfield/internal/core/runtime"
	"springfield/internal/features/ralph"
)

// Full-chain integration: workspace.RunNext → RuntimeExecutor → runtime.Runner → fakeCommandFunc
// Verifies the real invocation path end-to-end with hermetic doubles.

func captureCommandFunc(calls *[]exec.Command, exitCode int) exec.CommandFunc {
	return func(_ context.Context, cmd exec.Command, handler exec.EventHandler) exec.Result {
		*calls = append(*calls, cmd)
		if handler != nil {
			handler(exec.Event{Type: exec.EventStdout, Data: "output", Time: time.Now()})
		}
		return exec.Result{ExitCode: exitCode}
	}
}

func TestFullChainRalphRunNextPassesStoryThroughRuntime(t *testing.T) {
	dir := t.TempDir()
	workspace, err := ralph.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	spec := ralph.Spec{
		Project:     "springfield",
		Description: "test plan",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Bootstrap", Description: "implement bootstrap feature"},
		},
	}
	if err := workspace.InitPlan("test", spec); err != nil {
		t.Fatalf("init plan: %v", err)
	}

	var calls []exec.Command
	registry := agents.NewRegistry(
		claude.New(fakeLookPath),
		codex.New(fakeLookPath),
	)
	runner := runtime.NewTestRunner(registry, captureCommandFunc(&calls, 0), time.Now)
	executor := ralph.NewRuntimeExecutor(runner, []agents.ID{agents.AgentClaude}, dir)

	record, err := workspace.RunNext("test", executor)
	if err != nil {
		t.Fatalf("run next: %v", err)
	}

	if record.Status != "passed" {
		t.Fatalf("record status: got %q want passed", record.Status)
	}

	if record.Agent != "claude" {
		t.Fatalf("record agent: got %q want claude", record.Agent)
	}

	if record.StoryID != "US-001" {
		t.Fatalf("record story: got %q want US-001", record.StoryID)
	}

	// Verify command was dispatched with story description as prompt
	if len(calls) != 1 {
		t.Fatalf("expected 1 command dispatch, got %d", len(calls))
	}

	// Claude adapter should produce: claude -p <prompt> --output-format stream-json
	cmdStr := strings.Join(append([]string{calls[0].Name}, calls[0].Args...), " ")
	if !strings.Contains(cmdStr, "bootstrap") {
		t.Fatalf("expected story description in command, got: %s", cmdStr)
	}

	// Story should be marked passed in plan
	reloaded, err := workspace.LoadPlan("test")
	if err != nil {
		t.Fatalf("reload plan: %v", err)
	}
	if !reloaded.Spec.Stories[0].Passed {
		t.Fatal("expected story to be marked passed after successful run")
	}
}

func TestFullChainRalphRunNextRecordsFailure(t *testing.T) {
	dir := t.TempDir()
	workspace, err := ralph.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	spec := ralph.Spec{
		Project: "springfield",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Bootstrap", Description: "implement bootstrap"},
		},
	}
	if err := workspace.InitPlan("test", spec); err != nil {
		t.Fatalf("init plan: %v", err)
	}

	var calls []exec.Command
	registry := agents.NewRegistry(
		claude.New(fakeLookPath),
		codex.New(fakeLookPath),
	)
	runner := runtime.NewTestRunner(registry, captureCommandFunc(&calls, 1), time.Now)
	executor := ralph.NewRuntimeExecutor(runner, []agents.ID{agents.AgentClaude}, dir)

	record, err := workspace.RunNext("test", executor)
	if err != nil {
		t.Fatalf("run next: %v", err)
	}

	if record.Status != "failed" {
		t.Fatalf("record status: got %q want failed", record.Status)
	}

	if record.Error == "" {
		t.Fatal("expected error message in failed run record")
	}

	if record.Agent != "claude" {
		t.Fatalf("record agent: got %q want claude", record.Agent)
	}

	// Story should remain unpassed
	reloaded, err := workspace.LoadPlan("test")
	if err != nil {
		t.Fatalf("reload plan: %v", err)
	}
	if reloaded.Spec.Stories[0].Passed {
		t.Fatal("expected story to remain unpassed after failed run")
	}
}

func TestFullChainRalphRunNextWithCodexAgent(t *testing.T) {
	dir := t.TempDir()
	workspace, err := ralph.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	spec := ralph.Spec{
		Project: "springfield",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Bootstrap", Description: "implement bootstrap"},
		},
	}
	if err := workspace.InitPlan("test", spec); err != nil {
		t.Fatalf("init plan: %v", err)
	}

	var calls []exec.Command
	registry := agents.NewRegistry(
		claude.New(fakeLookPath),
		codex.New(fakeLookPath),
	)
	runner := runtime.NewTestRunner(registry, captureCommandFunc(&calls, 0), time.Now)
	executor := ralph.NewRuntimeExecutor(runner, []agents.ID{agents.AgentCodex}, dir)

	record, err := workspace.RunNext("test", executor)
	if err != nil {
		t.Fatalf("run next: %v", err)
	}

	if record.Agent != "codex" {
		t.Fatalf("record agent: got %q want codex", record.Agent)
	}

	// Codex adapter should produce: codex -q <prompt>
	if len(calls) != 1 {
		t.Fatalf("expected 1 command dispatch, got %d", len(calls))
	}
	if calls[0].Name != "codex" {
		t.Fatalf("expected codex binary, got: %s", calls[0].Name)
	}
}

func TestFullChainRalphRunNextHonorsDependencies(t *testing.T) {
	dir := t.TempDir()
	workspace, err := ralph.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	spec := ralph.Spec{
		Project: "springfield",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Bootstrap", Description: "first"},
			{ID: "US-002", Title: "Config", Description: "second", DependsOn: []string{"US-001"}},
		},
	}
	if err := workspace.InitPlan("test", spec); err != nil {
		t.Fatalf("init plan: %v", err)
	}

	var calls []exec.Command
	registry := agents.NewRegistry(claude.New(fakeLookPath), codex.New(fakeLookPath))
	runner := runtime.NewTestRunner(registry, captureCommandFunc(&calls, 0), time.Now)
	executor := ralph.NewRuntimeExecutor(runner, []agents.ID{agents.AgentClaude}, dir)

	// First run should pick US-001
	record1, err := workspace.RunNext("test", executor)
	if err != nil {
		t.Fatalf("run first: %v", err)
	}
	if record1.StoryID != "US-001" {
		t.Fatalf("first run: got %q want US-001", record1.StoryID)
	}

	// Second run should pick US-002 (dependency satisfied)
	record2, err := workspace.RunNext("test", executor)
	if err != nil {
		t.Fatalf("run second: %v", err)
	}
	if record2.StoryID != "US-002" {
		t.Fatalf("second run: got %q want US-002", record2.StoryID)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 total command dispatches, got %d", len(calls))
	}
}
