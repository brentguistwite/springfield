package execution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

	// maxExecutionPromptBytes caps the assembled execution prompt delivered via
	// stdin. 200 KB is a conservative budget that catches runaway AGENTS.md /
	// source.md content before process launch.
	maxExecutionPromptBytes = 200 * 1024

	// maxGuidanceFileBytes caps each project guidance file read (AGENTS.md,
	// CLAUDE.md, GEMINI.md). Three files at this limit = 192 KB, safely under
	// maxExecutionPromptBytes when combined with the rest of the prompt.
	maxGuidanceFileBytes = 64 * 1024
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
	if len(loaded.Config.Project.AgentPriority) == 0 {
		return Runner{}, fmt.Errorf(
			"project has no agents configured: agent_priority is empty. Run \"springfield init\" to select agents.")
	}

	registry := agents.NewRegistry(
		claude.New(lookPath),
		codex.New(lookPath),
		gemini.New(lookPath),
	)
	runtimeRunner := coreruntime.NewRunner(registry)
	agentIDs := priorityAgentIDs(loaded.Config.Project.AgentPriority)
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
	prompt, err := executionPrompt(root, work, workstream)
	if err != nil {
		return Report{}, err
	}
	if len(prompt) > maxExecutionPromptBytes {
		return Report{}, fmt.Errorf("execution prompt too large (%d bytes > %d byte limit): reduce AGENTS.md/CLAUDE.md/GEMINI.md or source.md size", len(prompt), maxExecutionPromptBytes)
	}
	result := e.runner.Run(context.Background(), coreruntime.Request{
		AgentIDs:          e.agents,
		Prompt:            prompt,
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
			AgentID:     string(result.Agent),
			ExitCode:    result.ExitCode,
		}, err
	}

	return Report{
		Status:      statusCompleted,
		Workstreams: []WorkstreamRun{outcome},
		AgentID:     string(result.Agent),
		ExitCode:    result.ExitCode,
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

		outcome, err := e.executeWorkstream(root, work, workstream)
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

func (e runtimeMultiExecutor) executeWorkstream(root string, work Work, workstream Workstream) (WorkstreamRun, error) {
	prompt, err := executionPrompt(root, work, workstream)
	if err != nil {
		return WorkstreamRun{Name: workstream.Name, Status: statusFailed}, err
	}
	if len(prompt) > maxExecutionPromptBytes {
		return WorkstreamRun{Name: workstream.Name, Status: statusFailed}, fmt.Errorf("execution prompt too large (%d bytes > %d byte limit): reduce AGENTS.md/CLAUDE.md/GEMINI.md or source.md size", len(prompt), maxExecutionPromptBytes)
	}
	result := e.runner.Run(context.Background(), coreruntime.Request{
		AgentIDs:          e.agents,
		Prompt:            prompt,
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

func executionPrompt(root string, work Work, workstream Workstream) (string, error) {
	var b strings.Builder
	b.WriteString("You are executing one slice of an approved Springfield batch.\n")
	b.WriteString("\n# Slice\n")
	fmt.Fprintf(&b, "- ID: %s\n", workstream.Name)
	fmt.Fprintf(&b, "- Title: %s\n", workstream.Title)
	if strings.TrimSpace(workstream.Summary) != "" {
		b.WriteString("- Body:\n")
		b.WriteString(strings.TrimSpace(workstream.Summary))
		b.WriteString("\n")
	}
	b.WriteString("\n# Batch context\n")
	fmt.Fprintf(&b, "Title: %s\n", work.Title)
	if strings.TrimSpace(work.RequestBody) != "" {
		b.WriteString("Original request:\n")
		b.WriteString(strings.TrimSpace(work.RequestBody))
		b.WriteString("\n")
	}
	guidance, err := readProjectGuidance(root)
	if err != nil {
		return "", err
	}
	if guidance != "" {
		b.WriteString("\n# Project context\n")
		b.WriteString(guidance)
	}
	b.WriteString("\n# Contract\n")
	b.WriteString("- Implement the slice end-to-end: code, tests, commit when green.\n")
	b.WriteString("- Do NOT invoke `Skill(springfield:*)` — those are user-facing surfaces, not for you.\n")
	b.WriteString("- Do NOT run `springfield start`, `springfield plan`, `springfield recover` from Bash. You are already inside a springfield-managed run.\n")
	b.WriteString("- Do NOT read, write, edit, or delete files under `.springfield/` — that is springfield's control plane.\n")
	b.WriteString("- When the slice is done, exit without asking for confirmation.\n")
	return b.String(), nil
}

// readProjectGuidance reads AGENTS.md, CLAUDE.md, GEMINI.md from root in that
// priority order, capped at maxGuidanceFileBytes each. Missing files (ENOENT)
// are silently skipped. Any other read error fails loudly — silently dropping
// guardrail instructions would let the subagent run unconstrained.
func readProjectGuidance(root string) (string, error) {
	files := []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"}
	var b strings.Builder
	for _, name := range files {
		f, err := os.Open(filepath.Join(root, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("read project guidance %s: %w", name, err)
		}
		// Read one byte beyond the cap to detect truncation.
		data, readErr := io.ReadAll(io.LimitReader(f, int64(maxGuidanceFileBytes)+1))
		f.Close()
		if readErr != nil {
			return "", fmt.Errorf("read project guidance %s: %w", name, readErr)
		}
		if len(data) > maxGuidanceFileBytes {
			return "", fmt.Errorf("project guidance file %s exceeds %d byte limit (%d bytes); reduce file size or remove it", name, maxGuidanceFileBytes, len(data))
		}
		fmt.Fprintf(&b, "## %s\n%s\n", name, string(data))
	}
	return b.String(), nil
}
