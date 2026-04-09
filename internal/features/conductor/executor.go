package conductor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"springfield/internal/core/agents"
	"springfield/internal/core/exec"
	"springfield/internal/core/runtime"
)

// RuntimeExecutor implements PlanExecutor using the shared runtime boundary.
type RuntimeExecutor struct {
	runner         runtime.Runner
	agents         []agents.ID
	plansDir       string
	legacyPlansDir string
	workDir        string
	settings       agents.ExecutionSettings
	OnEvent        exec.EventHandler
}

// NewRuntimeExecutor creates a PlanExecutor backed by the shared runtime.
func NewRuntimeExecutor(
	runner runtime.Runner,
	agents []agents.ID,
	plansDir, workDir string,
	settings agents.ExecutionSettings,
) *RuntimeExecutor {
	return &RuntimeExecutor{
		runner:         runner,
		agents:         agents,
		plansDir:       plansDir,
		legacyPlansDir: legacyPlansFallback(plansDir),
		workDir:        workDir,
		settings:       settings,
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
	var lastErr error
	for _, dir := range e.planDirs() {
		planPath := filepath.Join(dir, plan+".md")
		content, err := os.ReadFile(planPath)
		if err == nil {
			return content, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			lastErr = err
			continue
		}
		return nil, fmt.Errorf("read plan %s: %w", plan, err)
	}
	return nil, fmt.Errorf("read plan %s: %w", plan, lastErr)
}

func (e *RuntimeExecutor) planDirs() []string {
	if e.legacyPlansDir == "" {
		return []string{e.plansDir}
	}
	return []string{e.plansDir, e.legacyPlansDir}
}

func legacyPlansFallback(plansDir string) string {
	clean := filepath.Clean(plansDir)
	local := filepath.Clean(LocalPlansDir)
	legacy := filepath.Clean(legacyLocalPlansDir)
	if filepath.IsAbs(clean) {
		suffix := string(os.PathSeparator) + local
		if strings.HasSuffix(clean, suffix) {
			return filepath.Join(strings.TrimSuffix(clean, suffix), legacy)
		}
		return ""
	}
	if clean == local {
		return legacy
	}
	return ""
}
