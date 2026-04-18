package execution

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
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
)

const (
	statusReady     = "ready"
	statusCompleted = "completed"
	statusFailed    = "failed"
)

// Runner routes Springfield work to the correct internal execution engine.
type Runner struct {
	Single SingleExecutor
	Multi  MultiExecutor
}

// NewRuntimeRunner builds Springfield runtime adapters over the internal engines.
func NewRuntimeRunner(root string, lookPath func(string) (string, error), onEvent coreexec.EventHandler) (Runner, error) {
	loaded, err := config.LoadFrom(root)
	if err != nil {
		return Runner{}, err
	}

	registry := agents.NewRegistry(
		claude.New(lookPath),
		codex.New(lookPath),
		gemini.New(lookPath),
	)
	runtimeRunner := coreruntime.NewRunner(registry)
	agentIDs := priorityAgentIDs(loaded.Config.EffectivePriority())
	settings := loaded.Config.ExecutionSettings()

	return Runner{
		Single: runtimeSingleExecutor{
			runner:   runtimeRunner,
			agents:   agentIDs,
			workDir:  loaded.RootDir,
			settings: settings,
			onEvent:  onEvent,
		},
		Multi: runtimeMultiExecutor{
			runner:   runtimeRunner,
			agents:   agentIDs,
			workDir:  loaded.RootDir,
			settings: settings,
			onEvent:  onEvent,
		},
	}, nil
}

// Run executes Springfield work through the appropriate internal engine.
func (r Runner) Run(root string, work Work) (Report, error) {
	switch work.Split {
	case "single":
		if r.Single == nil {
			return Report{}, errors.New("single executor is not configured")
		}
		return r.Single.Run(root, work)
	case "multi":
		if r.Multi == nil {
			return Report{}, errors.New("multi executor is not configured")
		}
		return r.Multi.Run(root, work)
	default:
		return Report{}, fmt.Errorf("unsupported work split %q", work.Split)
	}
}

type runtimeSingleExecutor struct {
	runner   coreruntime.Runner
	agents   []agents.ID
	workDir  string
	settings agents.ExecutionSettings
	onEvent  coreexec.EventHandler
}

func (e runtimeSingleExecutor) Run(root string, work Work) (Report, error) {
	if len(work.Workstreams) != 1 {
		return Report{}, fmt.Errorf("work %q split %q requires exactly one workstream, got %d", work.ID, work.Split, len(work.Workstreams))
	}

	workstream := work.Workstreams[0]
	result := e.runner.Run(context.Background(), coreruntime.Request{
		AgentIDs:          e.agents,
		Prompt:            executionPrompt(work, workstream),
		WorkDir:           e.workDir,
		OnEvent:           e.onEvent,
		ExecutionSettings: e.settings,
	})

	outcome := WorkstreamRun{
		Name:   workstream.Name,
		Status: statusCompleted,
	}
	if err := errorFromResult(result); err != nil {
		outcome.Status = statusFailed
		outcome.Error = err.Error()
		return Report{
			Status:      statusFailed,
			Error:       outcome.Error,
			Workstreams: []WorkstreamRun{outcome},
		}, err
	}

	return Report{
		Status:      statusCompleted,
		Workstreams: []WorkstreamRun{outcome},
	}, nil
}

func errorFromResult(result coreruntime.Result) error {
	if result.Status == coreruntime.StatusFailed {
		if result.Err != nil {
			return fmt.Errorf("agent %s failed: %w", result.Agent, result.Err)
		}
		return fmt.Errorf("agent %s exited with code %d", result.Agent, result.ExitCode)
	}
	return nil
}

type runtimeMultiExecutor struct {
	runner   coreruntime.Runner
	agents   []agents.ID
	workDir  string
	settings agents.ExecutionSettings
	onEvent  coreexec.EventHandler
}

func (e runtimeMultiExecutor) Run(root string, work Work) (Report, error) {
	schedule := conductor.BuildSchedule(&conductor.Config{
		Batches: [][]string{workstreamNames(work)},
	})
	state := conductor.NewState()
	seedConductorState(state, work)

	next := schedule.NextPlans(state)
	results := make(map[string]WorkstreamRun, len(work.Workstreams))
	var runErr error
	firstError := ""

	for _, name := range next {
		workstream, ok := findWorkstream(work, name)
		if !ok {
			if runErr == nil {
				runErr = fmt.Errorf("workstream %q not found", name)
			}
			if firstError == "" {
				firstError = runErr.Error()
			}
			results[name] = WorkstreamRun{Name: name, Status: statusFailed, Error: runErr.Error()}
			continue
		}

		outcome, err := e.executeWorkstream(work, workstream)
		results[name] = outcome
		if err != nil {
			if runErr == nil {
				runErr = err
			}
			if firstError == "" {
				firstError = outcome.Error
			}
			state.Plans[name] = &conductor.PlanState{
				Status:       conductor.StatusFailed,
				Error:        outcome.Error,
				EvidencePath: outcome.EvidencePath,
			}
			continue
		}

		state.Plans[name] = &conductor.PlanState{Status: conductor.StatusCompleted}
	}

	ordered := make([]WorkstreamRun, 0, len(work.Workstreams))
	for _, workstream := range work.Workstreams {
		if result, ok := results[workstream.Name]; ok {
			ordered = append(ordered, result)
			continue
		}
		if workstream.Status == statusCompleted {
			ordered = append(ordered, WorkstreamRun{Name: workstream.Name, Status: statusCompleted})
			continue
		}
		ordered = append(ordered, WorkstreamRun{Name: workstream.Name, Status: statusReady})
	}

	status := statusCompleted
	if !schedule.IsComplete(state) {
		status = statusFailed
		if firstError == "" {
			firstError = "execution failed"
		}
	}

	return Report{
		Status:      status,
		Error:       firstError,
		Workstreams: ordered,
	}, runErr
}

func (e runtimeMultiExecutor) executeWorkstream(work Work, workstream Workstream) (WorkstreamRun, error) {
	result := e.runner.Run(context.Background(), coreruntime.Request{
		AgentIDs:          e.agents,
		Prompt:            executionPrompt(work, workstream),
		WorkDir:           e.workDir,
		OnEvent:           e.onEvent,
		ExecutionSettings: e.settings,
	})

	outcome := WorkstreamRun{
		Name:   workstream.Name,
		Status: statusCompleted,
	}
	if result.Status == coreruntime.StatusFailed {
		outcome.Status = statusFailed
		if result.Err != nil {
			outcome.Error = result.Err.Error()
			return outcome, fmt.Errorf("workstream %s: %w", workstream.Name, result.Err)
		}
		outcome.Error = fmt.Sprintf("agent exited with code %d", result.ExitCode)
		return outcome, errors.New(outcome.Error)
	}

	return outcome, nil
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

func workstreamNames(work Work) []string {
	names := make([]string, 0, len(work.Workstreams))
	for _, workstream := range work.Workstreams {
		names = append(names, workstream.Name)
	}
	return names
}

func seedConductorState(state *conductor.State, work Work) {
	for _, workstream := range work.Workstreams {
		if workstream.Status != statusCompleted {
			continue
		}
		state.Plans[workstream.Name] = &conductor.PlanState{Status: conductor.StatusCompleted}
	}
}

func findWorkstream(work Work, name string) (Workstream, bool) {
	for _, workstream := range work.Workstreams {
		if workstream.Name == name {
			return workstream, true
		}
	}
	return Workstream{}, false
}

func executionPrompt(work Work, workstream Workstream) string {
	var builder strings.Builder
	builder.WriteString("Springfield approved work\n\n")
	fmt.Fprintf(&builder, "Work ID: %s\n", work.ID)
	fmt.Fprintf(&builder, "Title: %s\n", work.Title)
	if strings.TrimSpace(work.RequestBody) != "" {
		builder.WriteString("\nOriginal request:\n")
		builder.WriteString(strings.TrimSpace(work.RequestBody))
		builder.WriteString("\n")
	}
	builder.WriteString("\nExecute this workstream:\n")
	fmt.Fprintf(&builder, "- Name: %s\n", workstream.Name)
	fmt.Fprintf(&builder, "- Title: %s\n", workstream.Title)
	if strings.TrimSpace(workstream.Summary) != "" {
		fmt.Fprintf(&builder, "- Summary: %s\n", workstream.Summary)
	}
	builder.WriteString("\nKeep Springfield as the user-facing surface.\n")
	builder.WriteString("\nDo not read, modify, or remove files under `.springfield/` — it is Springfield's control plane.\n")
	return builder.String()
}
