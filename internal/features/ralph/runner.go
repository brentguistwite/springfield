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
	agents  []agents.ID
	workDir string
	OnEvent exec.EventHandler
}

// NewRuntimeExecutor creates a StoryExecutor backed by the shared runtime.
func NewRuntimeExecutor(runner runtime.Runner, agents []agents.ID, workDir string) RuntimeExecutor {
	return RuntimeExecutor{
		runner:  runner,
		agents:  agents,
		workDir: workDir,
	}
}

// Execute runs a story through the shared runtime and returns a structured result.
func (e RuntimeExecutor) Execute(story Story) RunResult {
	result := e.runner.Run(context.Background(), runtime.Request{
		AgentIDs: e.agents,
		Prompt:   story.Description,
		WorkDir:  e.workDir,
		OnEvent:  e.OnEvent,
	})

	out := RunResult{
		Agent:    string(result.Agent),
		ExitCode: result.ExitCode,
	}

	if result.Status == runtime.StatusFailed {
		if result.Err != nil {
			out.Err = fmt.Errorf("agent %s failed: %w", result.Agent, result.Err)
		} else {
			out.Err = fmt.Errorf("agent %s exited with code %d", result.Agent, result.ExitCode)
		}
	}

	return out
}
