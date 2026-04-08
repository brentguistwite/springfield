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
	agentIDs := normalizeAgentIDs(req.AgentIDs)
	if len(agentIDs) == 0 {
		return Result{
			Status:    StatusFailed,
			Err:       fmt.Errorf("runtime request missing agent chain"),
			StartedAt: start,
			EndedAt:   r.now(),
		}
	}

	var last Result
	for _, agentID := range agentIDs {
		resolved, err := r.registry.Resolve(agents.ResolveInput{ProjectDefault: agentID})
		if err != nil {
			return Result{
				Agent:     agentID,
				Status:    StatusFailed,
				Err:       fmt.Errorf("resolve agent: %w", err),
				StartedAt: start,
				EndedAt:   r.now(),
			}
		}

		commander, ok := resolved.Adapter.(agents.Commander)
		if !ok {
			return Result{
				Agent:     agentID,
				Status:    StatusFailed,
				Err:       fmt.Errorf("agent %q does not support command execution", agentID),
				StartedAt: start,
				EndedAt:   r.now(),
			}
		}

		cmd := commander.Command(agents.CommandInput{
			Prompt:            req.Prompt,
			WorkDir:           req.WorkDir,
			ExecutionSettings: req.ExecutionSettings,
		})
		cmd.Timeout = req.Timeout

		execResult := r.run(ctx, cmd, req.OnEvent)
		status := StatusPassed
		if execResult.ExitCode != 0 || execResult.Err != nil {
			status = StatusFailed
		}

		last = Result{
			Agent:     agentID,
			Status:    status,
			ExitCode:  execResult.ExitCode,
			Events:    execResult.Events,
			Err:       execResult.Err,
			StartedAt: start,
			EndedAt:   r.now(),
		}
		if status == StatusPassed {
			return last
		}
		if !IsRateLimitError(execResult.Err, execResult.Events) {
			return last
		}
	}

	return last
}

func normalizeAgentIDs(ids []agents.ID) []agents.ID {
	out := make([]agents.ID, 0, len(ids))
	seen := map[agents.ID]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
