package runtime_test

import (
	"context"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
)

func envTestRegistry() agents.Registry {
	return agents.NewRegistry(claude.New(func(name string) (string, error) {
		return "/usr/local/bin/" + name, nil
	}))
}

// TestRunInjectsRequestEnvIntoCommand proves Request.Env (the slice's port
// block) reaches the built agent command's Env map, where the exec layer merges
// it over os.Environ() for the child process.
func TestRunInjectsRequestEnvIntoCommand(t *testing.T) {
	var captured exec.Command
	runFn := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		captured = cmd
		return exec.Result{ExitCode: 0, Events: []exec.Event{{
			Type: exec.EventStdout,
			Data: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1"},{"type":"tool_result","tool_use_id":"t1","is_error":false}]}}`,
			Time: time.Now(),
		}}}
	}
	runner := coreruntime.NewTestRunner(envTestRegistry(), runFn, time.Now)
	runner.Run(context.Background(), coreruntime.Request{
		AgentIDs: []agents.ID{agents.AgentClaude},
		Prompt:   "do the work",
		WorkDir:  t.TempDir(),
		Env:      map[string]string{"SPRINGFIELD_PORT": "42010", "SPRINGFIELD_PORT_RANGE": "42010-42019"},
	})
	if got := captured.Env["SPRINGFIELD_PORT"]; got != "42010" {
		t.Fatalf("cmd.Env[SPRINGFIELD_PORT] = %q, want 42010", got)
	}
	if got := captured.Env["SPRINGFIELD_PORT_RANGE"]; got != "42010-42019" {
		t.Fatalf("cmd.Env[SPRINGFIELD_PORT_RANGE] = %q, want 42010-42019", got)
	}
}
