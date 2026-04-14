package workflow

import (
	"errors"
	"fmt"

	coreexec "springfield/internal/core/exec"
	"springfield/internal/features/execution"
	"springfield/internal/storage"
)

const (
	statusReady     = "ready"
	statusRunning   = "running"
	statusCompleted = "completed"
	statusFailed    = "failed"
	statusDraft     = "draft"
)

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

// Runner executes Springfield work through the execution adapter boundary.
type Runner struct {
	Executor execution.Executor
}

// Run executes Springfield work through the Springfield execution adapter.
func (r Runner) Run(root, workID string) (RunResult, error) {
	return r.run(root, workID)
}

// Resume continues Springfield work through the Springfield execution adapter.
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

func (r Runner) execute(root string, work Work) (execution.Report, error) {
	if r.Executor == nil {
		return execution.Report{}, errors.New("execution adapter is not configured")
	}
	return r.Executor.Run(root, toExecutionWork(work))
}

func mergeReport(work Work, report execution.Report, execErr error) execution.Report {
	byName := make(map[string]execution.WorkstreamRun, len(report.Workstreams))
	for _, workstream := range report.Workstreams {
		if workstream.Name == "" {
			continue
		}
		byName[workstream.Name] = workstream
	}

	merged := execution.Report{
		Status:      normalizedReportStatus(report.Status),
		Workstreams: make([]execution.WorkstreamRun, 0, len(work.Workstreams)),
	}

	firstError := ""
	for _, workstream := range work.Workstreams {
		current, ok := byName[workstream.Name]
		if !ok {
			current = execution.WorkstreamRun{Name: workstream.Name, Status: workstream.Status}
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

	merged.Error = firstError
	return reportWithError(merged, firstError)
}

func reportWithError(report execution.Report, err string) execution.Report {
	if err == "" {
		return report
	}
	for i := range report.Workstreams {
		if report.Workstreams[i].Status == statusFailed && report.Workstreams[i].Error == "" {
			report.Workstreams[i].Error = err
			return report
		}
	}
	if len(report.Workstreams) == 1 && report.Workstreams[0].Error == "" {
		report.Workstreams[0].Error = err
	}
	return report
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

// NewRuntimeRunner builds a workflow runner over Springfield's execution adapter.
func NewRuntimeRunner(root string, lookPath func(string) (string, error), onEvent coreexec.EventHandler) (Runner, error) {
	executor, err := execution.NewRuntimeRunner(root, lookPath, onEvent)
	if err != nil {
		return Runner{}, err
	}
	return Runner{Executor: executor}, nil
}

func toExecutionWork(work Work) execution.Work {
	workstreams := make([]execution.Workstream, 0, len(work.Workstreams))
	for _, workstream := range work.Workstreams {
		workstreams = append(workstreams, execution.Workstream{
			Name:         workstream.Name,
			Title:        workstream.Title,
			Summary:      workstream.Summary,
			Status:       workstream.Status,
			Error:        workstream.Error,
			EvidencePath: workstream.EvidencePath,
		})
	}

	return execution.Work{
		ID:          work.ID,
		Title:       work.Title,
		RequestBody: work.RequestBody,
		Split:       work.Split,
		Workstreams: workstreams,
	}
}
