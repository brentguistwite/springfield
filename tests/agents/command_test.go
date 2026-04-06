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

	// Must include the prompt
	assertArgsContain(t, cmd.Args, "-q", "fix the auth bug")
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
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return
		}
	}

	t.Fatalf("expected args to contain %q %q, got %v", flag, value, args)
}
