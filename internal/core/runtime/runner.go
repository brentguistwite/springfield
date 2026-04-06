package runtime

import (
	"context"
	"fmt"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/exec"
)

// Runner resolves an agent and executes a prompt through it.
type Runner struct {
	registry agents.Registry
	run      exec.CommandFunc
	now      func() time.Time
}

// NewRunner creates a Runner with production defaults.
func NewRunner(registry agents.Registry) Runner {
	return Runner{
		registry: registry,
		run:      exec.Run,
		now:      time.Now,
	}
}

// NewTestRunner creates a Runner with injectable command execution and clock.
func NewTestRunner(registry agents.Registry, runFn exec.CommandFunc, clock func() time.Time) Runner {
	return Runner{
		registry: registry,
		run:      runFn,
		now:      clock,
	}
}

// Run resolves the agent, builds a command, and executes it.
func (r Runner) Run(ctx context.Context, req Request) Result {
	start := r.now()

	resolved, err := r.registry.Resolve(agents.ResolveInput{
		ProjectDefault: req.AgentID,
	})
	if err != nil {
		return Result{
			Agent:     req.AgentID,
			Status:    StatusFailed,
			Err:       fmt.Errorf("resolve agent: %w", err),
			StartedAt: start,
			EndedAt:   r.now(),
		}
	}

	commander, ok := resolved.Adapter.(agents.Commander)
	if !ok {
		return Result{
			Agent:     req.AgentID,
			Status:    StatusFailed,
			Err:       fmt.Errorf("agent %q does not support command execution", req.AgentID),
			StartedAt: start,
			EndedAt:   r.now(),
		}
	}

	cmd := commander.Command(agents.CommandInput{
		Prompt:  req.Prompt,
		WorkDir: req.WorkDir,
	})
	cmd.Timeout = req.Timeout

	execResult := r.run(ctx, cmd, req.OnEvent)

	status := StatusPassed
	if execResult.ExitCode != 0 || execResult.Err != nil {
		status = StatusFailed
	}

	return Result{
		Agent:     req.AgentID,
		Status:    status,
		ExitCode:  execResult.ExitCode,
		Events:    execResult.Events,
		Err:       execResult.Err,
		StartedAt: start,
		EndedAt:   r.now(),
	}
}
