package tui

import (
	"springfield/internal/core/config"
	"springfield/internal/features/doctor"
	"springfield/internal/features/planner"
)

// Screen identifies which TUI screen is active.
type Screen int

const (
	ScreenHome Screen = iota
	ScreenSetup
	ScreenNewWork
	ScreenStatus
	ScreenAdvancedSetup
	ScreenDoctor
)

// NavigateMsg tells the app to switch screens.
type NavigateMsg struct {
	Screen Screen
}

// BackMsg tells the app to return to the home screen.
type BackMsg struct{}

// SetupStatus summarizes local project readiness for the guided setup shell.
type SetupStatus struct {
	WorkingDir          string
	ProjectRoot         string
	ConfigPath          string
	RuntimeDir          string
	ExecutionConfigPath string
	ConfigPresent       bool
	RuntimePresent      bool
	ExecutionReady      bool
	Error               string
}

// NeedsInit reports whether core Springfield bootstrap files are still missing.
func (s SetupStatus) NeedsInit() bool {
	return !s.ConfigPresent || !s.RuntimePresent
}

// ExecutionConfigInput holds user-chosen execution setup options for the TUI.
type ExecutionConfigInput struct {
	PlansDir                   string
	WorktreeBase               string
	MaxRetries                 int
	SingleWorkstreamIterations int
	SingleWorkstreamTimeout    int
	UpdateGitignore            bool
}

// ExecutionConfigResult describes what the TUI execution setup action produced.
type ExecutionConfigResult struct {
	Created          bool
	Reused           bool
	Path             string
	GitignoreUpdated bool
}

// MonitorState tracks the lifecycle of an active TUI run.
type MonitorState int

const (
	MonitorIdle MonitorState = iota
	MonitorRunning
	MonitorSucceeded
	MonitorFailed
)

// RuntimeEvent is a TUI-safe projection of a single runtime output event.
type RuntimeEvent struct {
	Source string // "stdout" or "stderr"
	Data   string
}

// RuntimeEventMsg delivers a streaming event to the TUI during execution.
type RuntimeEventMsg struct {
	Event RuntimeEvent
}

// SpringfieldWorkstreamStatus is the TUI-safe projection of one Springfield workstream.
type SpringfieldWorkstreamStatus struct {
	Name         string
	Title        string
	Status       string
	Error        string
	EvidencePath string
}

// SpringfieldStatus captures the current Springfield execution surface state for the TUI.
type SpringfieldStatus struct {
	Ready       bool
	Reason      string
	WorkID      string
	Title       string
	Split       string
	Status      string
	Workstreams []SpringfieldWorkstreamStatus
}

// SpringfieldDiagnosisFailure is the TUI-safe projection of one Springfield failure.
type SpringfieldDiagnosisFailure struct {
	Workstream   string
	Title        string
	Error        string
	EvidencePath string
}

// SpringfieldDiagnosis captures Springfield-owned failure guidance for the TUI.
type SpringfieldDiagnosis struct {
	WorkID             string
	Status             string
	Summary            string
	EvidencePath       string
	FailingWorkstreams []string
	LastError          string
	NextStep           string
	Failures           []SpringfieldDiagnosisFailure
}

// SpringfieldRunResult describes the outcome of a TUI-initiated Springfield run.
type SpringfieldRunResult struct {
	WorkID string
	Status string
	Error  string
}

// SpringfieldRunCompleteMsg signals that an async Springfield run finished.
type SpringfieldRunCompleteMsg struct {
	Result SpringfieldRunResult
	Err    error
}

// AgentDetection is a TUI-safe projection of agent availability.
type AgentDetection struct {
	ID        string
	Name      string
	Installed bool
}

type AgentExecutionModes struct {
	Claude string
	Codex  string
}

type SaveAgentExecutionModesInput struct {
	Claude string
	Codex  string
}

// PlannedWorkstreamSummary is the TUI-safe projection of one planned workstream.
type PlannedWorkstreamSummary struct {
	Name    string
	Title   string
	Summary string
}

// PlannedWorkDraft is the TUI-safe review model for one planner-produced draft.
type PlannedWorkDraft struct {
	WorkID      string
	Title       string
	Summary     string
	Split       planner.Split
	Workstreams []PlannedWorkstreamSummary
}

// PlanWorkResult describes the current planner outcome for the TUI.
type PlanWorkResult struct {
	Question string
	Draft    *PlannedWorkDraft
}

// ExecutionConfig is a TUI-safe projection of current execution settings.
type ExecutionConfig struct {
	PlansDir                   string
	WorktreeBase               string
	MaxRetries                 int
	SingleWorkstreamIterations int
	SingleWorkstreamTimeout    int
}

// Services hides TUI data loading and side effects behind a small boundary.
type Services interface {
	SetupStatus() SetupStatus
	InitProject() (config.InitResult, error)
	ConfigureExecution(opts ExecutionConfigInput) (ExecutionConfigResult, error)
	DetectAgents() []AgentDetection
	AgentPriority() []string
	AgentExecutionModes() AgentExecutionModes
	CurrentExecutionConfig() *ExecutionConfig
	SpringfieldStatus() SpringfieldStatus
	SpringfieldDiagnosis() SpringfieldDiagnosis
	RunSpringfieldWork(onEvent func(RuntimeEvent)) (SpringfieldRunResult, error)
	ResumeSpringfieldWork(onEvent func(RuntimeEvent)) (SpringfieldRunResult, error)
	SaveAgentPriority(priority []string) error
	SaveAgentExecutionModes(input SaveAgentExecutionModesInput) error
	EnsureRecommendedExecutionDefaults() error
	UpdateExecutionConfig(opts ExecutionConfigInput) (ExecutionConfigResult, error)
	DoctorSummary() doctor.Report
	PlanWork(input string) (PlanWorkResult, error)
	RegeneratePlannedWork() (PlanWorkResult, error)
	ApprovePlannedWork() error
	ResetPlannedWork()
}
