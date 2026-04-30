package planner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
	"springfield/internal/core/config"
	coreexec "springfield/internal/core/exec"
	"springfield/internal/core/runtime"
)

type RuntimeRunner struct {
	projectRoot string
	lookPath    func(string) (string, error)
}

func NewRuntimeRunner(projectRoot string, lookPath func(string) (string, error)) RuntimeRunner {
	return RuntimeRunner{
		projectRoot: projectRoot,
		lookPath:    lookPath,
	}
}

func (r RuntimeRunner) Run(prompt string) (string, error) {
	registry := agents.NewRegistry(
		claude.New(r.lookPath),
		codex.New(r.lookPath),
		gemini.New(r.lookPath),
	)
	priority, settings, err := r.loadConfig()
	if err != nil {
		return "", err
	}

	result := runtime.NewRunner(registry).Run(context.Background(), runtime.Request{
		AgentIDs:          priority,
		Prompt:            prompt,
		WorkDir:           r.projectRoot,
		ExecutionSettings: settings,
	})
	if result.Err != nil {
		return "", result.Err
	}
	if result.Status != runtime.StatusPassed {
		return "", fmt.Errorf("planner agent %q failed", result.Agent)
	}

	lines := make([]string, 0, len(result.Events))
	for _, event := range result.Events {
		if event.Type != coreexec.EventStdout {
			continue
		}
		lines = append(lines, event.Data)
	}

	output := strings.TrimSpace(strings.Join(lines, "\n"))
	if output == "" {
		return "", fmt.Errorf("planner agent %q returned no stdout", result.Agent)
	}
	return output, nil
}

func (r RuntimeRunner) loadConfig() ([]agents.ID, agents.ExecutionSettings, error) {
	loaded, err := config.LoadFrom(r.projectRoot)
	if err == nil {
		if len(loaded.Config.Project.AgentPriority) == 0 {
			return nil, agents.ExecutionSettings{}, fmt.Errorf(
				"project has no agents configured: agent_priority is empty. Run \"springfield init\" to select agents.")
		}
		return priorityAgentIDs(loaded.Config.Project.AgentPriority), loaded.Config.ExecutionSettings(), nil
	}

	var missing *config.MissingConfigError
	if errors.As(err, &missing) {
		return []agents.ID{agents.AgentClaude, agents.AgentCodex, agents.AgentGemini}, agents.ExecutionSettings{}, nil
	}

	return nil, agents.ExecutionSettings{}, err
}

func priorityAgentIDs(priority []string) []agents.ID {
	ids := make([]agents.ID, 0, len(priority))
	for _, id := range priority {
		if id == "" {
			continue
		}
		ids = append(ids, agents.ID(id))
	}
	return ids
}
