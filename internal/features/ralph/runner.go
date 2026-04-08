package ralph

import (
	"context"
	"fmt"
	"strings"

	"springfield/internal/core/agents"
	"springfield/internal/core/exec"
	"springfield/internal/core/runtime"
)

// RuntimeExecutor implements StoryExecutor using the shared runtime boundary.
type RuntimeExecutor struct {
	runner   runtime.Runner
	agents   []agents.ID
	workDir  string
	settings agents.ExecutionSettings
	OnEvent  exec.EventHandler
}

// NewRuntimeExecutor creates a StoryExecutor backed by the shared runtime.
func NewRuntimeExecutor(
	runner runtime.Runner,
	agents []agents.ID,
	workDir string,
	settings agents.ExecutionSettings,
) RuntimeExecutor {
	return RuntimeExecutor{
		runner:   runner,
		agents:   agents,
		workDir:  workDir,
		settings: settings,
	}
}

// Execute runs a story through the shared runtime and returns a structured result.
func (e RuntimeExecutor) Execute(story Story) RunResult {
	result := e.runner.Run(context.Background(), runtime.Request{
		AgentIDs:          e.agents,
		Prompt:            story.Description,
		WorkDir:           e.workDir,
		OnEvent:           e.OnEvent,
		ExecutionSettings: e.settings,
	})

	stdout, stderr := collectOutput(result.Events)

	out := RunResult{
		Agent:    string(result.Agent),
		ExitCode: result.ExitCode,
		Stdout:   stdout,
		Stderr:   stderr,
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

func collectOutput(events []exec.Event) (stdout, stderr string) {
	var out, err strings.Builder
	for _, e := range events {
		switch e.Type {
		case exec.EventStdout:
			if out.Len() > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(e.Data)
		case exec.EventStderr:
			if err.Len() > 0 {
				err.WriteByte('\n')
			}
			err.WriteString(e.Data)
		}
	}
	return out.String(), err.String()
}
