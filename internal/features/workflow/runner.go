package workflow

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
	"springfield/internal/features/ralph"
	"springfield/internal/storage"
)

const (
	statusReady     = "ready"
	statusRunning   = "running"
	statusCompleted = "completed"
	statusFailed    = "failed"
	statusDraft     = "draft"
)

// WorkstreamRun captures one Springfield workstream execution outcome.
type WorkstreamRun struct {
	Name         string
	Status       string
	Error        string
	EvidencePath string
}

// ExecutionReport is the Springfield-owned adapter output from an internal engine.
type ExecutionReport struct {
	Status      string
	Error       string
	Workstreams []WorkstreamRun
}

// RunResult is the Springfield-owned summary of one run or resume attempt.
type RunResult struct {
	WorkID string
	Status string
	Error  string
}

// WorkstreamStatus is the public Springfield status projection for one workstream.
type WorkstreamStatus struct {
	Name         string
	Title        string
	Status       string
	Error        string
	EvidencePath string
}

// Status is the Springfield-owned status view for one persisted work item.
type Status struct {
	WorkID      string
	Title       string
	Split       string
	Status      string
	Approved    bool
	Workstreams []WorkstreamStatus
}

// DiagnosisFailure captures one failing Springfield workstream.
type DiagnosisFailure struct {
	Workstream   string
	Title        string
	Error        string
	EvidencePath string
}

// Diagnosis is the Springfield-owned failure view and next-step guidance.
type Diagnosis struct {
	WorkID             string
	Status             string
	Summary            string
	EvidencePath       string
	FailingWorkstreams []string
	LastError          string
	NextStep           string
	Failures           []DiagnosisFailure
}

// SingleExecutor runs one single-stream Springfield work item.
type SingleExecutor interface {
	Run(root string, work Work) (ExecutionReport, error)
}

// MultiExecutor runs one multi-stream Springfield work item.
type MultiExecutor interface {
	Run(root string, work Work) (ExecutionReport, error)
}

// Runner chooses the internal execution engine behind the Springfield boundary.
type Runner struct {
	Single SingleExecutor
	Multi  MultiExecutor
}

// Run executes Springfield work through the appropriate internal engine.
func (r Runner) Run(root, workID string) (RunResult, error) {
	return r.run(root, workID)
}

// Resume continues Springfield work through the appropriate internal engine.
func (r Runner) Resume(root, workID string) (RunResult, error) {
	return r.run(root, workID)
}

// Status returns the Springfield-owned status view for one work item.
func (r Runner) Status(root, workID string) (Status, error) {
	work, state, err := loadWorkState(root, workID)
	if err != nil {
		return Status{}, err
	}

	status := Status{
		WorkID:      work.ID,
		Title:       work.Title,
		Split:       work.Split,
		Status:      publicStatus(state.Status, state.Approved),
		Approved:    state.Approved,
		Workstreams: make([]WorkstreamStatus, 0, len(work.Workstreams)),
	}
	for _, workstream := range work.Workstreams {
		status.Workstreams = append(status.Workstreams, WorkstreamStatus{
			Name:         workstream.Name,
			Title:        workstream.Title,
			Status:       workstream.Status,
			Error:        workstream.Error,
			EvidencePath: workstream.EvidencePath,
		})
	}

	return status, nil
}

// Diagnose returns the Springfield-owned diagnosis for one work item.
func (r Runner) Diagnose(root, workID string) (Diagnosis, error) {
	status, err := r.Status(root, workID)
	if err != nil {
		return Diagnosis{}, err
	}

	failures := make([]DiagnosisFailure, 0)
	failingWorkstreams := make([]string, 0)
	evidencePath := ""
	lastError := ""
	for _, workstream := range status.Workstreams {
		if workstream.Status != statusFailed {
			continue
		}
		failingWorkstreams = append(failingWorkstreams, workstream.Name)
		if evidencePath == "" && workstream.EvidencePath != "" {
			evidencePath = workstream.EvidencePath
		}
		if lastError == "" && workstream.Error != "" {
			lastError = workstream.Error
		}
		failures = append(failures, DiagnosisFailure{
			Workstream:   workstream.Name,
			Title:        workstream.Title,
			Error:        workstream.Error,
			EvidencePath: workstream.EvidencePath,
		})
	}

	return Diagnosis{
		WorkID:             status.WorkID,
		Status:             status.Status,
		Summary:            diagnosisSummary(status.Status, len(failures)),
		EvidencePath:       evidencePath,
		FailingWorkstreams: failingWorkstreams,
		LastError:          lastError,
		NextStep:           nextStep(status.Status, len(failures)),
		Failures:           failures,
	}, nil
}

func (r Runner) run(root, workID string) (RunResult, error) {
	work, state, err := loadWorkState(root, workID)
	if err != nil {
		return RunResult{}, err
	}
	if !state.Approved {
		return RunResult{}, fmt.Errorf("work %q is not approved", workID)
	}

	report, execErr := r.execute(root, work)
	merged := mergeReport(work, report, execErr)
	state.Status = merged.Status
	state.Error = merged.Error
	state.WorkstreamStates = make([]workstreamStatusFile, 0, len(merged.Workstreams))
	for _, workstream := range merged.Workstreams {
		state.WorkstreamStates = append(state.WorkstreamStates, workstreamStatusFile{
			Name:         workstream.Name,
			Status:       workstream.Status,
			Error:        workstream.Error,
			EvidencePath: workstream.EvidencePath,
		})
	}

	if err := writeRunState(root, work.ID, state); err != nil {
		return RunResult{}, err
	}

	result := RunResult{
		WorkID: work.ID,
		Status: merged.Status,
		Error:  merged.Error,
	}
	if merged.Status == statusFailed {
		if execErr != nil {
			return result, execErr
		}
		if merged.Error != "" {
			return result, errors.New(merged.Error)
		}
		return result, fmt.Errorf("work %q failed", work.ID)
	}

	return result, nil
}

func (r Runner) execute(root string, work Work) (ExecutionReport, error) {
	switch work.Split {
	case "single":
		if r.Single == nil {
			return ExecutionReport{}, errors.New("single executor is not configured")
		}
		return r.Single.Run(root, work)
	case "multi":
		if r.Multi == nil {
			return ExecutionReport{}, errors.New("multi executor is not configured")
		}
		return r.Multi.Run(root, work)
	default:
		return ExecutionReport{}, fmt.Errorf("unsupported work split %q", work.Split)
	}
}

func mergeReport(work Work, report ExecutionReport, execErr error) ExecutionReport {
	byName := make(map[string]WorkstreamRun, len(report.Workstreams))
	for _, workstream := range report.Workstreams {
		if workstream.Name == "" {
			continue
		}
		byName[workstream.Name] = workstream
	}

	merged := ExecutionReport{
		Status:      normalizedReportStatus(report.Status),
		Workstreams: make([]WorkstreamRun, 0, len(work.Workstreams)),
	}

	firstError := ""
	for _, workstream := range work.Workstreams {
		current, ok := byName[workstream.Name]
		if !ok {
			current = WorkstreamRun{Name: workstream.Name, Status: workstream.Status}
			if current.Status == "" {
				current.Status = defaultWorkstreamStatus(report.Status)
			}
		}
		if current.Status == "" {
			current.Status = defaultWorkstreamStatus(report.Status)
		}
		if current.Status == statusFailed && firstError == "" {
			firstError = current.Error
		}
		merged.Workstreams = append(merged.Workstreams, current)
	}

	if execErr != nil && firstError == "" {
		firstError = execErr.Error()
	}

	if merged.Status == statusCompleted {
		for _, workstream := range merged.Workstreams {
			if workstream.Status == statusFailed {
				merged.Status = statusFailed
				break
			}
		}
	}
	if execErr != nil {
		merged.Status = statusFailed
	}

	if merged.Status == statusFailed && firstError == "" {
		firstError = "execution failed"
	}

	return ExecutionReport{
		Status:      merged.Status,
		Error:       firstError,
		Workstreams: merged.Workstreams,
	}.withError(firstError)
}

func (r ExecutionReport) withError(err string) ExecutionReport {
	if err == "" {
		return r
	}
	// Abuse the first failed workstream when the engine returned only a top-level failure.
	for i := range r.Workstreams {
		if r.Workstreams[i].Status == statusFailed && r.Workstreams[i].Error == "" {
			r.Workstreams[i].Error = err
			return r
		}
	}
	if len(r.Workstreams) == 1 && r.Workstreams[0].Error == "" {
		r.Workstreams[0].Error = err
	}
	return r
}

func normalizedReportStatus(status string) string {
	switch status {
	case statusCompleted, statusFailed, statusRunning:
		return status
	default:
		return statusCompleted
	}
}

func defaultWorkstreamStatus(reportStatus string) string {
	if normalizedReportStatus(reportStatus) == statusFailed {
		return statusFailed
	}
	return statusCompleted
}

func nextStep(status string, failures int) string {
	switch {
	case status == statusCompleted:
		return "Work completed successfully."
	case failures > 0:
		return "Review the failing workstreams, then resume the work."
	default:
		return "Resume the work to continue execution."
	}
}

func diagnosisSummary(status string, failures int) string {
	switch {
	case status == statusCompleted:
		return "Springfield work completed successfully."
	case failures == 1:
		return "1 Springfield workstream failed."
	case failures > 1:
		return fmt.Sprintf("%d Springfield workstreams failed.", failures)
	case status == statusFailed:
		return "Springfield work failed."
	default:
		return "No Springfield failures detected."
	}
}

func publicStatus(status string, approved bool) string {
	switch status {
	case statusCompleted, statusFailed, statusRunning:
		return status
	case statusDraft:
		if approved {
			return statusReady
		}
	}
	if approved {
		return statusReady
	}
	return status
}

func loadWorkState(root, workID string) (Work, runStateFile, error) {
	work, err := LoadWork(root, workID)
	if err != nil {
		return Work{}, runStateFile{}, err
	}

	rt, err := storage.FromRoot(root)
	if err != nil {
		return Work{}, runStateFile{}, fmt.Errorf("resolve runtime: %w", err)
	}
	workPaths, err := rt.Work(workID)
	if err != nil {
		return Work{}, runStateFile{}, fmt.Errorf("resolve work paths: %w", err)
	}
	state, err := readRunState(workPaths.RunStatePath())
	if err != nil {
		return Work{}, runStateFile{}, err
	}

	return work, state, nil
}

func writeRunState(root, workID string, state runStateFile) error {
	rt, err := storage.FromRoot(root)
	if err != nil {
		return fmt.Errorf("resolve runtime: %w", err)
	}
	workPaths, err := rt.Work(workID)
	if err != nil {
		return fmt.Errorf("resolve work paths: %w", err)
	}
	if err := writeJSONFile(workPaths.RunStatePath(), state); err != nil {
		return err
	}
	return nil
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

	single := ralph.NewRuntimeExecutor(runtimeRunner, agentIDs, loaded.RootDir, settings)
	single.OnEvent = onEvent

	return Runner{
		Single: runtimeSingleExecutor{executor: single},
		Multi: runtimeMultiExecutor{
			runner:   runtimeRunner,
			agents:   agentIDs,
			workDir:  loaded.RootDir,
			settings: settings,
			onEvent:  onEvent,
		},
	}, nil
}

type runtimeSingleExecutor struct {
	executor ralph.RuntimeExecutor
}

func (e runtimeSingleExecutor) Run(root string, work Work) (ExecutionReport, error) {
	if len(work.Workstreams) == 0 {
		return ExecutionReport{}, errors.New("single work has no workstreams")
	}

	workstream := work.Workstreams[0]
	result := e.executor.Execute(ralph.Story{
		ID:          workstream.Name,
		Title:       workstream.Title,
		Description: executionPrompt(work, workstream),
	})

	outcome := WorkstreamRun{
		Name:   workstream.Name,
		Status: statusCompleted,
	}
	if result.Err != nil {
		outcome.Status = statusFailed
		outcome.Error = result.Err.Error()
		return ExecutionReport{
			Status:      statusFailed,
			Error:       outcome.Error,
			Workstreams: []WorkstreamRun{outcome},
		}, result.Err
	}

	return ExecutionReport{
		Status:      statusCompleted,
		Workstreams: []WorkstreamRun{outcome},
	}, nil
}

type runtimeMultiExecutor struct {
	runner   coreruntime.Runner
	agents   []agents.ID
	workDir  string
	settings agents.ExecutionSettings
	onEvent  coreexec.EventHandler
}

func (e runtimeMultiExecutor) Run(root string, work Work) (ExecutionReport, error) {
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

	return ExecutionReport{
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
	return builder.String()
}
