package tui

import (
	"springfield/internal/core/config"
	"springfield/internal/features/doctor"
)

// Screen identifies which TUI screen is active.
type Screen int

const (
	ScreenHome Screen = iota
	ScreenSetup
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

// ConductorSummary captures the current conductor surface state for the TUI.
type ConductorSummary struct {
	Ready     bool
	Reason    string
	Completed int
	Total     int
	Done      bool
	Failures  []string
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

// ConductorSetupResult describes what the TUI conductor setup action produced.
type ConductorSetupResult struct {
	Created bool
	Reused  bool
	Path    string
}

// Services hides TUI data loading and side effects behind a small boundary.
type Services interface {
	SetupStatus() SetupStatus
	InitProject() (config.InitResult, error)
	SetupConductor() (ConductorSetupResult, error)
	RalphSummary() RalphSummary
	RunRalphNext(planName string) (RalphRunResult, error)
	ConductorSummary() ConductorSummary
	RunConductorNext() (ConductorRunResult, error)
	DoctorSummary() doctor.Report
}
