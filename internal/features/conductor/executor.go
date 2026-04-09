package conductor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"springfield/internal/core/agents"
	"springfield/internal/core/exec"
	"springfield/internal/core/runtime"
)

// RuntimeExecutor implements PlanExecutor using the shared runtime boundary.
type RuntimeExecutor struct {
	runner   runtime.Runner
	agents   []agents.ID
	plansDir string
	workDir  string
	settings agents.ExecutionSettings
	OnEvent  exec.EventHandler
}

// NewRuntimeExecutor creates a PlanExecutor backed by the shared runtime.
func NewRuntimeExecutor(
	runner runtime.Runner,
	agents []agents.ID,
	plansDir, workDir string,
	settings agents.ExecutionSettings,
) *RuntimeExecutor {
	return &RuntimeExecutor{
		runner:   runner,
		agents:   agents,
		plansDir: plansDir,
		workDir:  workDir,
		settings: settings,
	}
}

// Execute reads the plan file, runs it through the shared runtime, and returns
// the agent used plus any evidence path.
func (e *RuntimeExecutor) Execute(plan string) (ExecuteResult, error) {
	content, err := e.readPlan(plan)
	if err != nil {
		return ExecuteResult{}, err
	}

	result := e.runner.Run(context.Background(), runtime.Request{
		AgentIDs:          e.agents,
		Prompt:            string(content),
		WorkDir:           e.workDir,
		OnEvent:           e.OnEvent,
		ExecutionSettings: e.settings,
	})

	out := ExecuteResult{
		Agent: string(result.Agent),
	}

	if result.Status == runtime.StatusFailed {
		if result.Err != nil {
			return out, fmt.Errorf("plan %s: %w", plan, result.Err)
		}
		return out, fmt.Errorf("plan %s: agent exited with code %d", plan, result.ExitCode)
	}

	return out, nil
}

func (e *RuntimeExecutor) readPlan(plan string) ([]byte, error) {
	planPath := filepath.Join(e.plansDir, plan+".md")
	content, err := os.ReadFile(planPath)
	if err != nil {
		return nil, fmt.Errorf("read plan %s: %w", plan, err)
	}
	return content, nil
}
