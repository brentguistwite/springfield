package agents_test

import (
	"os/exec"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
)

func TestClaudeAdapterProducesRunnableCommandSpec(t *testing.T) {
	adapter := claude.New(exec.LookPath)

	commander, ok := adapter.(agents.Commander)
	if !ok {
		t.Fatal("claude adapter does not implement Commander")
	}

	cmd := commander.Command(agents.CommandInput{
		Prompt:  "implement the login feature",
		WorkDir: "/tmp/project",
	})

	if cmd.Name != "claude" {
		t.Fatalf("expected binary %q, got %q", "claude", cmd.Name)
	}

	if cmd.Dir != "/tmp/project" {
		t.Fatalf("expected dir %q, got %q", "/tmp/project", cmd.Dir)
	}

	// Must include -p flag with the prompt and --output-format stream-json
	assertArgsContain(t, cmd.Args, "-p", "implement the login feature")
	assertArgsContain(t, cmd.Args, "--output-format", "stream-json")
	assertArgsContain(t, cmd.Args, "--verbose", "")
}

func TestCodexAdapterProducesRunnableCommandSpec(t *testing.T) {
	adapter := codex.New(exec.LookPath)

	commander, ok := adapter.(agents.Commander)
	if !ok {
		t.Fatal("codex adapter does not implement Commander")
	}

	cmd := commander.Command(agents.CommandInput{
		Prompt:  "fix the auth bug",
		WorkDir: "/tmp/project",
	})

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

	cmd := commander.Command(agents.CommandInput{
		Prompt:  "test prompt",
		WorkDir: "/tmp",
	})

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

	cmd := commander.Command(agents.CommandInput{
		Prompt:  "implement the login feature",
		WorkDir: "/tmp/project",
	})

	assertArgsDoNotContain(t, cmd.Args, "--permission-mode")
}

func TestClaudeAdapterAppendsPermissionModeWhenConfigured(t *testing.T) {
	adapter := claude.New(exec.LookPath)
	commander := adapter.(agents.Commander)

	cmd := commander.Command(agents.CommandInput{
		Prompt:  "implement the login feature",
		WorkDir: "/tmp/project",
		ExecutionSettings: agents.ExecutionSettings{
			Claude: agents.ClaudeExecutionSettings{PermissionMode: "bypassPermissions"},
		},
	})

	assertArgsContain(t, cmd.Args, "--permission-mode", "bypassPermissions")
}

func TestCodexAdapterUsesExecJsonByDefault(t *testing.T) {
	adapter := codex.New(exec.LookPath)
	commander := adapter.(agents.Commander)

	cmd := commander.Command(agents.CommandInput{
		Prompt:  "fix the auth bug",
		WorkDir: "/tmp/project",
	})

	assertArgsContain(t, cmd.Args, "exec", "")
	assertArgsContain(t, cmd.Args, "--json", "")
	assertArgsDoNotContain(t, cmd.Args, "-q")
	assertLastArgEquals(t, cmd.Args, "fix the auth bug")
}

func TestCodexAdapterAppendsSandboxAndApprovalWhenConfigured(t *testing.T) {
	adapter := codex.New(exec.LookPath)
	commander := adapter.(agents.Commander)

	cmd := commander.Command(agents.CommandInput{
		Prompt:  "fix the auth bug",
		WorkDir: "/tmp/project",
		ExecutionSettings: agents.ExecutionSettings{
			Codex: agents.CodexExecutionSettings{
				SandboxMode:    "workspace-write",
				ApprovalPolicy: "on-request",
			},
		},
	})

	assertArgsContain(t, cmd.Args, "-s", "workspace-write")
	assertArgsContain(t, cmd.Args, "-a", "on-request")
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
