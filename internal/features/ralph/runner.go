package ralph

import (
	"context"
	"fmt"

	"springfield/internal/core/agents"
	"springfield/internal/core/exec"
	"springfield/internal/core/runtime"
)

// RuntimeExecutor implements StoryExecutor using the shared runtime boundary.
type RuntimeExecutor struct {
	runner  runtime.Runner
	agent   agents.ID
	workDir string
	OnEvent exec.EventHandler
}

// NewRuntimeExecutor creates a StoryExecutor backed by the shared runtime.
func NewRuntimeExecutor(runner runtime.Runner, agent agents.ID, workDir string) RuntimeExecutor {
	return RuntimeExecutor{
		runner:  runner,
		agent:   agent,
		workDir: workDir,
	}
}

// Execute runs a story through the shared runtime and returns a structured result.
func (e RuntimeExecutor) Execute(story Story) RunResult {
	result := e.runner.Run(context.Background(), runtime.Request{
		AgentID: e.agent,
		Prompt:  story.Description,
		WorkDir: e.workDir,
		OnEvent: e.OnEvent,
	})

	out := RunResult{
		Agent:    string(result.Agent),
		ExitCode: result.ExitCode,
	}

	if result.Status == runtime.StatusFailed {
		if result.Err != nil {
			out.Err = fmt.Errorf("agent %s failed: %w", e.agent, result.Err)
		} else {
			out.Err = fmt.Errorf("agent %s exited with code %d", e.agent, result.ExitCode)
		}
	}

	return out
}
