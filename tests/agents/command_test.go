package agents_test

import (
	"os/exec"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	coreexec "springfield/internal/core/exec"
)

func TestClaudeAdapterProducesRunnableCommandSpec(t *testing.T) {
	adapter := claude.New(exec.LookPath)

	commander, ok := adapter.(agents.Commander)
	if !ok {
		t.Fatal("claude adapter does not implement Commander")
	}

	cmd, err := commander.Command(agents.CommandInput{
		Prompt:  "implement the login feature",
		WorkDir: "/tmp/project",
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}

	if cmd.Name != "claude" {
		t.Fatalf("expected binary %q, got %q", "claude", cmd.Name)
	}

	if cmd.Dir != "/tmp/project" {
		t.Fatalf("expected dir %q, got %q", "/tmp/project", cmd.Dir)
	}

	// -p enables print mode; prompt arrives via stdin, not as positional arg
	assertArgsContain(t, cmd.Args, "-p", "")
	if cmd.Stdin != "implement the login feature" {
		t.Fatalf("expected Stdin = %q, got %q", "implement the login feature", cmd.Stdin)
	}
	assertArgsContain(t, cmd.Args, "--output-format", "stream-json")
	assertArgsContain(t, cmd.Args, "--verbose", "")
}

func TestCodexAdapterProducesRunnableCommandSpec(t *testing.T) {
	adapter := codex.New(exec.LookPath)

	commander, ok := adapter.(agents.Commander)
	if !ok {
		t.Fatal("codex adapter does not implement Commander")
	}

	cmd, err := commander.Command(agents.CommandInput{
		Prompt:  "fix the auth bug",
		WorkDir: "/tmp/project",
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}

	if cmd.Name != "codex" {
		t.Fatalf("expected binary %q, got %q", "codex", cmd.Name)
	}

	if cmd.Dir != "/tmp/project" {
		t.Fatalf("expected dir %q, got %q", "/tmp/project", cmd.Dir)
	}

	assertArgsContain(t, cmd.Args, "exec", "")
	assertArgsContain(t, cmd.Args, "--json", "")
	assertLastArgEquals(t, cmd.Args, "fix the auth bug")
}

func TestRegistryResolvesCommander(t *testing.T) {
	lookPath := func(string) (string, error) { return "/usr/bin/claude", nil }
	registry := agents.NewRegistry(claude.New(lookPath))

	resolved, err := registry.Resolve(agents.ResolveInput{
		ProjectDefault: agents.AgentClaude,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	commander, ok := resolved.Adapter.(agents.Commander)
	if !ok {
		t.Fatal("resolved adapter does not implement Commander")
	}

	cmd, err := commander.Command(agents.CommandInput{
		Prompt:  "test prompt",
		WorkDir: "/tmp",
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}

	if cmd.Name != "claude" {
		t.Fatalf("expected binary %q, got %q", "claude", cmd.Name)
	}
}

func assertArgsContain(t *testing.T, args []string, flag, value string) {
	t.Helper()

	for i, a := range args {
		if a != flag {
			continue
		}
		if value == "" {
			return
		}
		if i+1 < len(args) && args[i+1] == value {
			return
		}
	}

	if value == "" {
		t.Fatalf("expected args to contain %q, got %v", flag, args)
	}
	t.Fatalf("expected args to contain %q %q, got %v", flag, value, args)
}

func TestClaudeAdapterOmitsPermissionModeByDefault(t *testing.T) {
	adapter := claude.New(exec.LookPath)
	commander := adapter.(agents.Commander)

	cmd, err := commander.Command(agents.CommandInput{
		Prompt:  "implement the login feature",
		WorkDir: "/tmp/project",
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}

	assertArgsDoNotContain(t, cmd.Args, "--permission-mode")
}

func TestClaudeAdapterAppendsPermissionModeWhenConfigured(t *testing.T) {
	adapter := claude.New(exec.LookPath)
	commander := adapter.(agents.Commander)

	cmd, err := commander.Command(agents.CommandInput{
		Prompt:  "implement the login feature",
		WorkDir: "/tmp/project",
		ExecutionSettings: agents.ExecutionSettings{
			Claude: agents.ClaudeExecutionSettings{PermissionMode: "bypassPermissions"},
		},
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}

	assertArgsContain(t, cmd.Args, "--permission-mode", "bypassPermissions")
}

func TestCodexAdapterUsesExecJsonByDefault(t *testing.T) {
	adapter := codex.New(exec.LookPath)
	commander := adapter.(agents.Commander)

	cmd, err := commander.Command(agents.CommandInput{
		Prompt:  "fix the auth bug",
		WorkDir: "/tmp/project",
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}

	assertArgsContain(t, cmd.Args, "exec", "")
	assertArgsContain(t, cmd.Args, "--json", "")
	assertArgsDoNotContain(t, cmd.Args, "-q")
	assertLastArgEquals(t, cmd.Args, "fix the auth bug")
}

func TestCodexAdapterAppendsSandboxAndApprovalWhenConfigured(t *testing.T) {
	adapter := codex.New(exec.LookPath)
	commander := adapter.(agents.Commander)

	cmd, err := commander.Command(agents.CommandInput{
		Prompt:  "fix the auth bug",
		WorkDir: "/tmp/project",
		ExecutionSettings: agents.ExecutionSettings{
			Codex: agents.CodexExecutionSettings{
				SandboxMode:    "workspace-write",
				ApprovalPolicy: "on-request",
			},
		},
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}

	assertArgsContain(t, cmd.Args, "-s", "workspace-write")
	assertArgsContain(t, cmd.Args, "-a", "on-request")
}

func TestClaudeValidatorRejectsRejectedToolCalls(t *testing.T) {
	adapter := claude.New(exec.LookPath)
	validator, ok := adapter.(agents.ResultValidator)
	if !ok {
		t.Fatal("claude adapter does not implement ResultValidator")
	}

	result := coreexec.Result{
		ExitCode: 0,
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01"}]}}`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"rejected","is_error":true,"tool_use_id":"toolu_01"}]}}`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"result","subtype":"success","is_error":false}`, Time: time.Now()},
		},
	}

	err := validator.ValidateResult(result)
	if err == nil {
		t.Fatal("expected validator to reject result with is_error tool calls")
	}
}

func TestClaudeValidatorAcceptsCleanRun(t *testing.T) {
	adapter := claude.New(exec.LookPath)
	validator := adapter.(agents.ResultValidator)

	result := coreexec.Result{
		ExitCode: 0,
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: `{"type":"assistant","message":{"content":[{"type":"text","text":"Done."}]}}`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"result","subtype":"success","is_error":false}`, Time: time.Now()},
		},
	}

	if err := validator.ValidateResult(result); err != nil {
		t.Fatalf("expected clean run to pass validation, got: %v", err)
	}
}

func TestClaudeValidatorAcceptsRecoverableToolFailure(t *testing.T) {
	adapter := claude.New(exec.LookPath)
	validator := adapter.(agents.ResultValidator)

	result := coreexec.Result{
		ExitCode: 0,
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01"}]}}`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"command exited 1","is_error":true,"tool_use_id":"toolu_01"}]}}`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"assistant","message":{"content":[{"type":"text","text":"I fixed the command and completed the task."}]}}`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"result","subtype":"success","is_error":false}`, Time: time.Now()},
		},
	}

	if err := validator.ValidateResult(result); err != nil {
		t.Fatalf("expected recoverable tool failure to pass validation, got: %v", err)
	}
}

func TestCodexValidatorRejectsFatalStderr(t *testing.T) {
	adapter := codex.New(exec.LookPath)
	validator, ok := adapter.(agents.ResultValidator)
	if !ok {
		t.Fatal("codex adapter does not implement ResultValidator")
	}

	result := coreexec.Result{
		ExitCode: 0,
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: `{"type":"turn.completed"}`, Time: time.Now()},
			{Type: coreexec.EventStderr, Data: `2026-04-08T13:57:19Z ERROR rmcp::transport::worker: worker quit with fatal: Transport channel closed`, Time: time.Now()},
		},
	}

	err := validator.ValidateResult(result)
	if err == nil {
		t.Fatal("expected validator to reject result with fatal stderr")
	}
}

func TestCodexValidatorAcceptsCleanRun(t *testing.T) {
	adapter := codex.New(exec.LookPath)
	validator := adapter.(agents.ResultValidator)

	result := coreexec.Result{
		ExitCode: 0,
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: `{"type":"turn.completed"}`, Time: time.Now()},
		},
	}

	if err := validator.ValidateResult(result); err != nil {
		t.Fatalf("expected clean run to pass validation, got: %v", err)
	}
}

func TestCodexValidatorAcceptsOptionalMCPAuthFailureWhenWorkCompleted(t *testing.T) {
	adapter := codex.New(exec.LookPath)
	validator := adapter.(agents.ResultValidator)

	result := coreexec.Result{
		ExitCode: 0,
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: `{"type":"thread.started","thread_id":"t_123"}`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"turn.started"}`, Time: time.Now()},
			{Type: coreexec.EventStderr, Data: `2026-04-08T16:34:12.577918Z ERROR rmcp::transport::worker: worker quit with fatal: Transport channel closed, when AuthRequired(AuthRequiredError { www_authenticate_header: "Bearer realm=\"OAuth\", error=\"invalid_token\", error_description=\"Missing or invalid access token\"" })`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"Checking the required startup skill, then I’ll answer directly."}}`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"sed -n '1,220p' /Users/brent.guistwite/.codex/superpowers/skills/using-superpowers/SKILL.md","aggregated_output":"...","exit_code":0,"status":"completed"}}`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"Hello."}}`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}`, Time: time.Now()},
		},
	}

	if err := validator.ValidateResult(result); err != nil {
		t.Fatalf("expected optional MCP auth failure to be ignored after completed work, got: %v", err)
	}
}

func TestCodexValidatorRejectsClarifyingQuestionWithoutWork(t *testing.T) {
	adapter := codex.New(exec.LookPath)
	validator := adapter.(agents.ResultValidator)

	result := coreexec.Result{
		ExitCode: 0,
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: `{"type":"thread.started","thread_id":"t_123"}`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"turn.started"}`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"Which file should I update first?"}}`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}`, Time: time.Now()},
		},
	}

	err := validator.ValidateResult(result)
	if err == nil {
		t.Fatal("expected validator to reject clarifying question without work")
	}
}

func TestCodexValidatorAcceptsQuestionAfterDoingWork(t *testing.T) {
	adapter := codex.New(exec.LookPath)
	validator := adapter.(agents.ResultValidator)

	result := coreexec.Result{
		ExitCode: 0,
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: `{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"go test ./...","aggregated_output":"ok","exit_code":0,"status":"completed"}}`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"I ran the tests. Do you want me to clean up the warnings too?"}}`, Time: time.Now()},
			{Type: coreexec.EventStdout, Data: `{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}`, Time: time.Now()},
		},
	}

	if err := validator.ValidateResult(result); err != nil {
		t.Fatalf("expected completed work to pass validation, got: %v", err)
	}
}

func assertArgsDoNotContain(t *testing.T, args []string, flag string) {
	t.Helper()

	for _, arg := range args {
		if arg == flag {
			t.Fatalf("expected args not to contain %q, got %v", flag, args)
		}
	}
}

func assertLastArgEquals(t *testing.T, args []string, want string) {
	t.Helper()

	if len(args) == 0 {
		t.Fatal("expected args to be non-empty")
	}
	if got := args[len(args)-1]; got != want {
		t.Fatalf("expected last arg %q, got %q (args=%v)", want, got, args)
	}
}
