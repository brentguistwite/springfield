package ralph

import (
	"context"
	"fmt"

	"springfield/internal/core/agents"
	"springfield/internal/core/runtime"
)

// RuntimeExecutor implements StoryExecutor using the shared runtime boundary.
type RuntimeExecutor struct {
	runner  runtime.Runner
	agent   agents.ID
	workDir string
}

// NewRuntimeExecutor creates a StoryExecutor backed by the shared runtime.
func NewRuntimeExecutor(runner runtime.Runner, agent agents.ID, workDir string) RuntimeExecutor {
	return RuntimeExecutor{
		runner:  runner,
		agent:   agent,
		workDir: workDir,
	}
}

// Execute runs a story through the shared runtime and returns an error on failure.
func (e RuntimeExecutor) Execute(story Story) error {
	result := e.runner.Run(context.Background(), runtime.Request{
		AgentID: e.agent,
		Prompt:  story.Description,
		WorkDir: e.workDir,
	})

	if result.Status == runtime.StatusFailed {
		if result.Err != nil {
			return fmt.Errorf("agent %s failed: %w", e.agent, result.Err)
		}
		return fmt.Errorf("agent %s exited with code %d", e.agent, result.ExitCode)
	}

	return nil
}
