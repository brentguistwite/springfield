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
	ScreenAdvancedSetup
	ScreenRalph
	ScreenConductor
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
	WorkingDir           string
	ProjectRoot          string
	ConfigPath           string
	RuntimeDir           string
	ConductorConfigPath  string
	ConfigPresent        bool
	RuntimePresent       bool
	ConductorConfigReady bool
	Error                string
}

// NeedsInit reports whether core Springfield bootstrap files are still missing.
func (s SetupStatus) NeedsInit() bool {
	return !s.ConfigPresent || !s.RuntimePresent
}

// RalphPlanSummary is the TUI-safe projection of one Ralph plan.
type RalphPlanSummary struct {
	Name           string
	Project        string
	StoryCount     int
	NextStoryID    string
	NextStoryTitle string
}

// RalphRunSummary is the TUI-safe projection of one Ralph run.
type RalphRunSummary struct {
	PlanName string
	StoryID  string
	Status   string
}

// RalphSummary captures the current Ralph surface state for the TUI.
type RalphSummary struct {
	Ready      bool
	Reason     string
	Plans      []RalphPlanSummary
	RecentRuns []RalphRunSummary
}

// ConductorPlanFailure describes one failed conductor plan with evidence.
type ConductorPlanFailure struct {
	Plan         string
	Error        string
	Agent        string
	EvidencePath string
	Attempts     int
}

// ConductorSummary captures the current conductor surface state for the TUI.
type ConductorSummary struct {
	Ready     bool
	Reason    string
	Completed int
	Total     int
	Done      bool
	Failures  []ConductorPlanFailure
	NextStep  string
}

// RalphRunResult describes the outcome of a TUI-initiated Ralph run.
type RalphRunResult struct {
	PlanName string
	StoryID  string
	Status   string
	Error    string
}

// ConductorRunResult describes the outcome of a TUI-initiated conductor run.
type ConductorRunResult struct {
	Ran   []string
	Done  bool
	Error string
}

// ConductorSetupInput holds user-chosen conductor setup options for the TUI.
type ConductorSetupInput struct {
	PlansDir        string
	WorktreeBase    string
	MaxRetries      int
	RalphIterations int
	RalphTimeout    int
	UpdateGitignore bool
}

// ConductorSetupResult describes what the TUI conductor setup action produced.
type ConductorSetupResult struct {
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

// RalphRunCompleteMsg signals that an async Ralph run finished.
type RalphRunCompleteMsg struct {
	Result RalphRunResult
	Err    error
}

// ConductorRunCompleteMsg signals that an async conductor run finished.
type ConductorRunCompleteMsg struct {
	Result ConductorRunResult
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

// ConductorCurrentConfig is a TUI-safe projection of current conductor settings.
type ConductorCurrentConfig struct {
	PlansDir        string
	WorktreeBase    string
	MaxRetries      int
	RalphIterations int
	RalphTimeout    int
}

// Services hides TUI data loading and side effects behind a small boundary.
type Services interface {
	SetupStatus() SetupStatus
	InitProject() (config.InitResult, error)
	SetupConductor(opts ConductorSetupInput) (ConductorSetupResult, error)
	DetectAgents() []AgentDetection
	AgentPriority() []string
	AgentExecutionModes() AgentExecutionModes
	ConductorCurrentConfig() *ConductorCurrentConfig
	RalphSummary() RalphSummary
	RunRalphNext(planName string, onEvent func(RuntimeEvent)) (RalphRunResult, error)
	ConductorSummary() ConductorSummary
	RunConductorNext(onEvent func(RuntimeEvent)) (ConductorRunResult, error)
	SaveAgentPriority(priority []string) error
	SaveAgentExecutionModes(input SaveAgentExecutionModesInput) error
	EnsureRecommendedExecutionDefaults() error
	UpdateConductor(opts ConductorSetupInput) (ConductorSetupResult, error)
	DoctorSummary() doctor.Report
	PlanWork(input string) (PlanWorkResult, error)
	RegeneratePlannedWork() (PlanWorkResult, error)
	ApprovePlannedWork() error
	ResetPlannedWork()
}
