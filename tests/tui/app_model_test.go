package tui_test

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"springfield/internal/core/config"
	"springfield/internal/features/doctor"
	"springfield/internal/features/tui"
)

type fakeServices struct {
	setup               tui.SetupStatus
	initResult          config.InitResult
	initErr             error
	conductorSetup      tui.ConductorSetupResult
	conductorSetupErr   error
	conductorSetupCalls int
	ralph               tui.RalphSummary
	conductor           tui.ConductorSummary
	report              doctor.Report
	initCalls           int
	ralphRunResult      tui.RalphRunResult
	ralphRunErr         error
	ralphRunCalls       int
	ralphRunPlan        string
	ralphRunEvents      []tui.RuntimeEvent
	conductorRunResult  tui.ConductorRunResult
	conductorRunErr     error
	conductorRunCalls   int
	conductorRunEvents  []tui.RuntimeEvent
	agentDetections      []tui.AgentDetection
	agentPriorityOrder   []string
	conductorCurrentCfg  *tui.ConductorCurrentConfig
	savePriorityCalls    int
	savePriorityArg      []string
	savePriorityErr      error
	updateConductorCalls int
	updateConductorResult tui.ConductorSetupResult
	updateConductorErr   error
}

func (f *fakeServices) SetupStatus() tui.SetupStatus {
	return f.setup
}

func (f *fakeServices) InitProject() (config.InitResult, error) {
	f.initCalls++
	if f.initErr == nil {
		f.setup.ConfigPresent = true
		f.setup.RuntimePresent = true
	}
	return f.initResult, f.initErr
}

func (f *fakeServices) SetupConductor(opts tui.ConductorSetupInput) (tui.ConductorSetupResult, error) {
	f.conductorSetupCalls++
	if f.conductorSetupErr == nil {
		f.setup.ConductorConfigReady = true
	}
	return f.conductorSetup, f.conductorSetupErr
}

func (f *fakeServices) RalphSummary() tui.RalphSummary {
	return f.ralph
}

func (f *fakeServices) RunRalphNext(planName string, onEvent func(tui.RuntimeEvent)) (tui.RalphRunResult, error) {
	f.ralphRunCalls++
	f.ralphRunPlan = planName
	if onEvent != nil {
		for _, e := range f.ralphRunEvents {
			onEvent(e)
		}
	}
	return f.ralphRunResult, f.ralphRunErr
}

func (f *fakeServices) ConductorSummary() tui.ConductorSummary {
	return f.conductor
}

func (f *fakeServices) RunConductorNext(onEvent func(tui.RuntimeEvent)) (tui.ConductorRunResult, error) {
	f.conductorRunCalls++
	if onEvent != nil {
		for _, e := range f.conductorRunEvents {
			onEvent(e)
		}
	}
	return f.conductorRunResult, f.conductorRunErr
}

func (f *fakeServices) DoctorSummary() doctor.Report {
	return f.report
}

func (f *fakeServices) DetectAgents() []tui.AgentDetection {
	return f.agentDetections
}

func (f *fakeServices) AgentPriority() []string {
	return f.agentPriorityOrder
}

func (f *fakeServices) ConductorCurrentConfig() *tui.ConductorCurrentConfig {
	return f.conductorCurrentCfg
}

func (f *fakeServices) SaveAgentPriority(priority []string) error {
	f.savePriorityCalls++
	f.savePriorityArg = priority
	return f.savePriorityErr
}

func (f *fakeServices) UpdateConductor(opts tui.ConductorSetupInput) (tui.ConductorSetupResult, error) {
	f.updateConductorCalls++
	return f.updateConductorResult, f.updateConductorErr
}

// updateModel processes a message and follows up on any returned command.
func updateModel(t *testing.T, model tui.Model, msg tea.Msg) tui.Model {
	t.Helper()

	next, cmd := model.Update(msg)
	updated, ok := next.(tui.Model)
	if !ok {
		t.Fatalf("expected tui.Model, got %T", next)
	}

	if cmd != nil {
		followUp := cmd()
		if followUp != nil {
			next, _ = updated.Update(followUp)
			updated, ok = next.(tui.Model)
			if !ok {
				t.Fatalf("expected tui.Model after command, got %T", next)
			}
		}
	}

	return updated
}

// sendMsg processes a message without following up on commands.
// Use for async flows where the cmd would block (channel-based streaming).
func sendMsg(t *testing.T, model tui.Model, msg tea.Msg) tui.Model {
	t.Helper()

	next, _ := model.Update(msg)
	updated, ok := next.(tui.Model)
	if !ok {
		t.Fatalf("expected tui.Model, got %T", next)
	}

	return updated
}

func TestModelStartsOnHomeScreen(t *testing.T) {
	model := tui.NewModel(&fakeServices{})
	view := model.View()

	for _, marker := range []string{"Springfield", "Guided Setup", "Advanced Setup", "Ralph", "Conductor", "Doctor"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected home view to contain %q, got:\n%s", marker, view)
		}
	}
}

func TestModelSetupFlowCreatesCoreState(t *testing.T) {
	services := &fakeServices{
		setup: tui.SetupStatus{
			WorkingDir:          "/tmp/demo",
			ProjectRoot:         "/tmp/demo",
			ConfigPath:          "/tmp/demo/springfield.toml",
			RuntimeDir:          "/tmp/demo/.springfield",
			ConductorConfigPath: "/tmp/demo/.springfield/conductor/config.json",
		},
		initResult: config.InitResult{
			ConfigCreated:     true,
			RuntimeDirCreated: true,
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	if !strings.Contains(view, "missing at /tmp/demo/springfield.toml") {
		t.Fatalf("expected missing config state, got:\n%s", view)
	}

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	view = model.View()

	if services.initCalls != 1 {
		t.Fatalf("expected one init call, got %d", services.initCalls)
	}
	for _, marker := range []string{"springfield.toml created: true", ".springfield created: true", "Basic", "Advanced"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected setup view to contain %q, got:\n%s", marker, view)
		}
	}
}

func TestModelRendersRalphSummary(t *testing.T) {
	model := tui.NewModel(&fakeServices{
		ralph: tui.RalphSummary{
			Ready: true,
			Plans: []tui.RalphPlanSummary{
				{Name: "refresh", StoryCount: 2, NextStoryID: "US-002", NextStoryTitle: "Refresh prompt"},
			},
			RecentRuns: []tui.RalphRunSummary{
				{PlanName: "refresh", StoryID: "US-001", Status: "passed"},
			},
		},
	})

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	for _, marker := range []string{"Ralph", "refresh", "US-002", "US-001", "passed"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected Ralph view to contain %q, got:\n%s", marker, view)
		}
	}
}

func TestModelRendersConductorSummary(t *testing.T) {
	model := tui.NewModel(&fakeServices{
		conductor: tui.ConductorSummary{
			Ready:     true,
			Completed: 1,
			Total:     3,
			Failures: []tui.ConductorPlanFailure{
				{Plan: "02-config", Error: "compile error", Agent: "claude", Attempts: 1},
			},
			NextStep: "Fix failures then resume",
		},
	})

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	for _, marker := range []string{"Conductor", "Progress: 1/3", "02-config", "compile error"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected Conductor view to contain %q, got:\n%s", marker, view)
		}
	}
}

func TestSetupScreenShowsActionableConductorPrompt(t *testing.T) {
	services := &fakeServices{
		setup: tui.SetupStatus{
			WorkingDir:           "/tmp/demo",
			ProjectRoot:          "/tmp/demo",
			ConfigPath:           "/tmp/demo/springfield.toml",
			RuntimeDir:           "/tmp/demo/.springfield",
			ConductorConfigPath:  "/tmp/demo/.springfield/conductor/config.json",
			ConfigPresent:        true,
			RuntimePresent:       true,
			ConductorConfigReady: false,
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	if strings.Contains(view, "hand") || strings.Contains(view, "manually") || strings.Contains(view, "Next add") {
		t.Fatalf("setup view should not suggest manual config editing, got:\n%s", view)
	}
	if !strings.Contains(view, "Basic") || !strings.Contains(view, "Advanced") {
		t.Fatalf("expected Basic/Advanced choice prompt, got:\n%s", view)
	}
}

func TestSetupScreenTriggersConductorSetup(t *testing.T) {
	services := &fakeServices{
		setup: tui.SetupStatus{
			WorkingDir:           "/tmp/demo",
			ProjectRoot:          "/tmp/demo",
			ConfigPath:           "/tmp/demo/springfield.toml",
			RuntimeDir:           "/tmp/demo/.springfield",
			ConductorConfigPath:  "/tmp/demo/.springfield/conductor/config.json",
			ConfigPresent:        true,
			RuntimePresent:       true,
			ConductorConfigReady: false,
		},
		conductorSetup: tui.ConductorSetupResult{
			Created: true,
			Path:    "/tmp/demo/.springfield/conductor/config.json",
		},
	}

	model := tui.NewModel(services)
	// Navigate to setup screen
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	// Press Enter to trigger conductor setup
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	if services.conductorSetupCalls != 1 {
		t.Fatalf("expected 1 conductor setup call, got %d", services.conductorSetupCalls)
	}

	view := model.View()
	for _, marker := range []string{"conductor config created", "Setup complete with defaults"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected setup view to contain %q, got:\n%s", marker, view)
		}
	}
}

func TestSetupScreenShowsConductorSetupFailure(t *testing.T) {
	services := &fakeServices{
		setup: tui.SetupStatus{
			WorkingDir:           "/tmp/demo",
			ProjectRoot:          "/tmp/demo",
			ConfigPath:           "/tmp/demo/springfield.toml",
			RuntimeDir:           "/tmp/demo/.springfield",
			ConductorConfigPath:  "/tmp/demo/.springfield/conductor/config.json",
			ConfigPresent:        true,
			RuntimePresent:       true,
			ConductorConfigReady: false,
		},
		conductorSetupErr: errors.New("permission denied"),
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	if !strings.Contains(view, "permission denied") {
		t.Fatalf("expected failure message in view, got:\n%s", view)
	}
}

func TestSetupScreenFullyReadyAfterConductorSetup(t *testing.T) {
	services := &fakeServices{
		setup: tui.SetupStatus{
			WorkingDir:           "/tmp/demo",
			ProjectRoot:          "/tmp/demo",
			ConfigPath:           "/tmp/demo/springfield.toml",
			RuntimeDir:           "/tmp/demo/.springfield",
			ConductorConfigPath:  "/tmp/demo/.springfield/conductor/config.json",
			ConfigPresent:        true,
			RuntimePresent:       true,
			ConductorConfigReady: false,
		},
		conductorSetup: tui.ConductorSetupResult{
			Created: true,
			Path:    "/tmp/demo/.springfield/conductor/config.json",
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	if !strings.Contains(view, "Setup complete with defaults") {
		t.Fatalf("expected setup complete message, got:\n%s", view)
	}
	// Conductor config should show ready
	if !strings.Contains(view, "ready at") {
		t.Fatalf("expected conductor config ready indicator, got:\n%s", view)
	}
}

// --- Ralph async run and monitor tests ---

func TestRalphScreenRunsNextStory(t *testing.T) {
	services := &fakeServices{
		ralph: tui.RalphSummary{
			Ready: true,
			Plans: []tui.RalphPlanSummary{
				{Name: "refresh", StoryCount: 3, NextStoryID: "US-001", NextStoryTitle: "Setup"},
			},
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	// Press 'r' — transitions to running state
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	view := model.View()
	if !strings.Contains(view, "running...") {
		t.Fatalf("expected running state after r, got:\n%s", view)
	}

	// Simulate run completion
	model = sendMsg(t, model, tui.RalphRunCompleteMsg{
		Result: tui.RalphRunResult{
			PlanName: "refresh",
			StoryID:  "US-001",
			Status:   "passed",
		},
	})

	view = model.View()
	for _, marker := range []string{"US-001", "passed", "succeeded"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected Ralph view to contain %q after run, got:\n%s", marker, view)
		}
	}
}

func TestRalphScreenShowsRunFailure(t *testing.T) {
	services := &fakeServices{
		ralph: tui.RalphSummary{
			Ready: true,
			Plans: []tui.RalphPlanSummary{
				{Name: "refresh", StoryCount: 3, NextStoryID: "US-001", NextStoryTitle: "Setup"},
			},
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	// Press 'r' to start
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	// Complete with error
	model = sendMsg(t, model, tui.RalphRunCompleteMsg{
		Err: errors.New("agent claude failed: exit code 1"),
	})

	view := model.View()
	if !strings.Contains(view, "agent claude failed") {
		t.Fatalf("expected failure message in Ralph view, got:\n%s", view)
	}
	if !strings.Contains(view, "failed") {
		t.Fatalf("expected failed monitor state, got:\n%s", view)
	}
}

func TestRalphScreenShowsStreamingEvents(t *testing.T) {
	services := &fakeServices{
		ralph: tui.RalphSummary{
			Ready: true,
			Plans: []tui.RalphPlanSummary{
				{Name: "refresh", StoryCount: 3, NextStoryID: "US-001", NextStoryTitle: "Setup"},
			},
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	// Start run
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	// Stream events
	model = sendMsg(t, model, tui.RuntimeEventMsg{Event: tui.RuntimeEvent{Source: "stdout", Data: "building package..."}})
	model = sendMsg(t, model, tui.RuntimeEventMsg{Event: tui.RuntimeEvent{Source: "stderr", Data: "warning: unused var"}})

	view := model.View()
	for _, marker := range []string{"Events:", "[stdout] building package...", "[stderr] warning: unused var"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected Ralph view to contain %q during streaming, got:\n%s", marker, view)
		}
	}

	// Complete
	model = sendMsg(t, model, tui.RalphRunCompleteMsg{
		Result: tui.RalphRunResult{PlanName: "refresh", StoryID: "US-001", Status: "passed"},
	})

	view = model.View()
	if !strings.Contains(view, "succeeded") {
		t.Fatalf("expected succeeded after completion, got:\n%s", view)
	}
	// Events should persist after completion
	if !strings.Contains(view, "[stdout] building package...") {
		t.Fatalf("expected events to persist after completion, got:\n%s", view)
	}
}

func TestRalphScreenMonitorIdleToRunningToSucceeded(t *testing.T) {
	services := &fakeServices{
		ralph: tui.RalphSummary{
			Ready: true,
			Plans: []tui.RalphPlanSummary{
				{Name: "plan-a", StoryCount: 1, NextStoryID: "US-001", NextStoryTitle: "First"},
			},
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	// Idle: no status line
	view := model.View()
	if strings.Contains(view, "Status:") {
		t.Fatalf("expected no status line when idle, got:\n%s", view)
	}

	// Running
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	view = model.View()
	if !strings.Contains(view, "running...") {
		t.Fatalf("expected running status, got:\n%s", view)
	}

	// Succeeded
	model = sendMsg(t, model, tui.RalphRunCompleteMsg{
		Result: tui.RalphRunResult{PlanName: "plan-a", StoryID: "US-001", Status: "passed"},
	})
	view = model.View()
	if !strings.Contains(view, "succeeded") {
		t.Fatalf("expected succeeded status, got:\n%s", view)
	}
}

func TestRalphScreenMonitorIdleToRunningToFailed(t *testing.T) {
	services := &fakeServices{
		ralph: tui.RalphSummary{
			Ready: true,
			Plans: []tui.RalphPlanSummary{
				{Name: "plan-a", StoryCount: 1, NextStoryID: "US-001", NextStoryTitle: "First"},
			},
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = sendMsg(t, model, tui.RalphRunCompleteMsg{
		Result: tui.RalphRunResult{PlanName: "plan-a", StoryID: "US-001", Status: "failed", Error: "compile error"},
	})

	view := model.View()
	if !strings.Contains(view, "Status: failed") {
		t.Fatalf("expected failed status, got:\n%s", view)
	}
}

func TestRalphScreenBlocksEscWhileRunning(t *testing.T) {
	services := &fakeServices{
		ralph: tui.RalphSummary{
			Ready: true,
			Plans: []tui.RalphPlanSummary{
				{Name: "plan-a", StoryCount: 1, NextStoryID: "US-001", NextStoryTitle: "First"},
			},
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	// Start run
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	// Try Esc while running — should stay on ralph screen
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	view := model.View()
	if !strings.Contains(view, "Ralph") || !strings.Contains(view, "running...") {
		t.Fatalf("Esc should be blocked during run, got:\n%s", view)
	}
}

func TestRalphScreenBlocksRunWhileRunning(t *testing.T) {
	services := &fakeServices{
		ralph: tui.RalphSummary{
			Ready: true,
			Plans: []tui.RalphPlanSummary{
				{Name: "plan-a", StoryCount: 1, NextStoryID: "US-001", NextStoryTitle: "First"},
			},
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	// Start first run
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	// Try 'r' again while running — should be ignored
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	view := model.View()
	if !strings.Contains(view, "running...") {
		t.Fatalf("second r should be ignored during run, got:\n%s", view)
	}
}

// --- Conductor async run and monitor tests ---

func TestConductorScreenRunsNextPhase(t *testing.T) {
	services := &fakeServices{
		conductor: tui.ConductorSummary{
			Ready:     true,
			Completed: 1,
			Total:     3,
			NextStep:  "Run: springfield conductor run",
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	// Press 'r' to start
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	view := model.View()
	if !strings.Contains(view, "running...") {
		t.Fatalf("expected running state, got:\n%s", view)
	}

	// Complete
	model = sendMsg(t, model, tui.ConductorRunCompleteMsg{
		Result: tui.ConductorRunResult{
			Ran:  []string{"02-conductor-runtime"},
			Done: false,
		},
	})

	view = model.View()
	if !strings.Contains(view, "02-conductor-runtime") {
		t.Fatalf("expected ran plan in Conductor view, got:\n%s", view)
	}
}

func TestConductorScreenShowsRunFailure(t *testing.T) {
	services := &fakeServices{
		conductor: tui.ConductorSummary{
			Ready:     true,
			Completed: 0,
			Total:     3,
			NextStep:  "Run: springfield conductor run",
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = sendMsg(t, model, tui.ConductorRunCompleteMsg{
		Err: errors.New("plan 01-ralph-runtime: compile error"),
	})

	view := model.View()
	if !strings.Contains(view, "compile error") {
		t.Fatalf("expected failure message in Conductor view, got:\n%s", view)
	}
}

func TestConductorScreenShowsStreamingEvents(t *testing.T) {
	services := &fakeServices{
		conductor: tui.ConductorSummary{
			Ready:     true,
			Completed: 0,
			Total:     3,
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	// Start run
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	// Stream events
	model = sendMsg(t, model, tui.RuntimeEventMsg{Event: tui.RuntimeEvent{Source: "stdout", Data: "executing plan 01..."}})
	model = sendMsg(t, model, tui.RuntimeEventMsg{Event: tui.RuntimeEvent{Source: "stdout", Data: "tests passed"}})

	view := model.View()
	for _, marker := range []string{"Events:", "[stdout] executing plan 01...", "[stdout] tests passed"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected Conductor view to contain %q during streaming, got:\n%s", marker, view)
		}
	}
}

func TestConductorScreenBlocksEscWhileRunning(t *testing.T) {
	services := &fakeServices{
		conductor: tui.ConductorSummary{
			Ready:     true,
			Completed: 0,
			Total:     3,
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEsc})

	view := model.View()
	if !strings.Contains(view, "Conductor") || !strings.Contains(view, "running...") {
		t.Fatalf("Esc should be blocked during run, got:\n%s", view)
	}
}

func TestRalphScreenNoCliDeadEnd(t *testing.T) {
	model := tui.NewModel(&fakeServices{
		ralph: tui.RalphSummary{
			Ready: true,
			Plans: []tui.RalphPlanSummary{
				{Name: "refresh", StoryCount: 2, NextStoryID: "US-001", NextStoryTitle: "Setup"},
			},
		},
	})

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	if strings.Contains(view, "springfield ralph --help") {
		t.Fatalf("Ralph screen should not contain CLI dead end, got:\n%s", view)
	}
	if !strings.Contains(view, "r run") {
		t.Fatalf("expected actionable hint 'r run' in Ralph view, got:\n%s", view)
	}
}

func TestConductorScreenNoCliDeadEnd(t *testing.T) {
	model := tui.NewModel(&fakeServices{
		conductor: tui.ConductorSummary{
			Ready:     true,
			Completed: 0,
			Total:     3,
		},
	})

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	if strings.Contains(view, "springfield conductor --help") {
		t.Fatalf("Conductor screen should not contain CLI dead end, got:\n%s", view)
	}
	if !strings.Contains(view, "r run") {
		t.Fatalf("expected actionable hint 'r run' in Conductor view, got:\n%s", view)
	}
}

// --- US-003: Diagnosis views and CLI dead-end removal ---

func TestConductorScreenDiagnosisViewToggle(t *testing.T) {
	services := &fakeServices{
		conductor: tui.ConductorSummary{
			Ready:     true,
			Completed: 1,
			Total:     3,
			Failures: []tui.ConductorPlanFailure{
				{Plan: "02-config", Error: "compile error", Agent: "claude", EvidencePath: "/tmp/.springfield/conductor/evidence/02-config", Attempts: 2},
			},
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	// Press 'd' to enter diagnosis view
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})

	view := model.View()
	for _, marker := range []string{"Diagnosis", "02-config", "compile error", "claude", "Attempts: 2"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected diagnosis view to contain %q, got:\n%s", marker, view)
		}
	}

	// Press 'd' again to toggle back
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})

	view = model.View()
	if strings.Contains(view, "Diagnosis") {
		t.Fatalf("expected to leave diagnosis view after second d, got:\n%s", view)
	}
}

func TestConductorScreenDiagnosisShowsEvidencePath(t *testing.T) {
	services := &fakeServices{
		conductor: tui.ConductorSummary{
			Ready:     true,
			Completed: 0,
			Total:     2,
			Failures: []tui.ConductorPlanFailure{
				{Plan: "01-runtime", Error: "exit code 1", Agent: "codex", EvidencePath: "/tmp/evidence/01-runtime", Attempts: 3},
			},
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})

	view := model.View()
	if !strings.Contains(view, "/tmp/evidence/01-runtime") {
		t.Fatalf("expected evidence path in diagnosis view, got:\n%s", view)
	}
}

func TestConductorScreenDiagnosisNoFailuresMessage(t *testing.T) {
	services := &fakeServices{
		conductor: tui.ConductorSummary{
			Ready:     true,
			Completed: 2,
			Total:     3,
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})

	view := model.View()
	if !strings.Contains(view, "No failures") {
		t.Fatalf("expected 'No failures' message in empty diagnosis view, got:\n%s", view)
	}
}

func TestConductorScreenNextStepNoCliReference(t *testing.T) {
	services := &fakeServices{
		conductor: tui.ConductorSummary{
			Ready:     true,
			Completed: 1,
			Total:     3,
			Failures: []tui.ConductorPlanFailure{
				{Plan: "02-config", Error: "compile error"},
			},
			NextStep: "Fix failures then run: springfield conductor resume",
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	// Should not show raw CLI commands in the TUI
	if strings.Contains(view, "springfield conductor") {
		t.Fatalf("conductor screen should not reference CLI commands, got:\n%s", view)
	}
}

func TestConductorScreenShowsDiagnoseHint(t *testing.T) {
	services := &fakeServices{
		conductor: tui.ConductorSummary{
			Ready:     true,
			Completed: 1,
			Total:     3,
			Failures: []tui.ConductorPlanFailure{
				{Plan: "02-config", Error: "compile error"},
			},
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	if !strings.Contains(view, "d diagnose") {
		t.Fatalf("expected 'd diagnose' hint when failures exist, got:\n%s", view)
	}
}

func TestConductorScreenAfterFailedRunShowsDiagnosis(t *testing.T) {
	services := &fakeServices{
		conductor: tui.ConductorSummary{
			Ready:     true,
			Completed: 0,
			Total:     3,
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	// Start run
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	// Complete with error
	model = sendMsg(t, model, tui.ConductorRunCompleteMsg{
		Result: tui.ConductorRunResult{
			Ran:   []string{"01-runtime"},
			Error: "plan 01-runtime: agent failed",
		},
	})

	view := model.View()
	if !strings.Contains(view, "failed") {
		t.Fatalf("expected failed status after error, got:\n%s", view)
	}
	if !strings.Contains(view, "agent failed") {
		t.Fatalf("expected error detail after failed run, got:\n%s", view)
	}
}

func TestHomeMenuShowsAdvancedSetup(t *testing.T) {
	model := tui.NewModel(&fakeServices{})
	view := model.View()
	if !strings.Contains(view, "Advanced Setup") {
		t.Fatalf("expected home view to contain 'Advanced Setup', got:\n%s", view)
	}
}

func TestAdvancedSetupRedirectsWhenNoConfig(t *testing.T) {
	services := &fakeServices{
		setup: tui.SetupStatus{
			WorkingDir:  "/tmp/demo",
			ProjectRoot: "/tmp/demo",
			ConfigPath:  "/tmp/demo/springfield.toml",
			// ConfigPresent: false — not initialized
		},
	}
	model := tui.NewModel(services)
	// Navigate to Advanced Setup (second item in menu)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	view := model.View()
	if !strings.Contains(view, "Guided Setup") {
		t.Fatalf("expected redirect to Guided Setup, got:\n%s", view)
	}
}

func TestAdvancedSetupStorageModeSelection(t *testing.T) {
	services := &fakeServices{
		setup: tui.SetupStatus{
			WorkingDir:           "/tmp/demo",
			ProjectRoot:          "/tmp/demo",
			ConfigPath:           "/tmp/demo/springfield.toml",
			RuntimeDir:           "/tmp/demo/.springfield",
			ConductorConfigPath:  "/tmp/demo/.springfield/conductor/config.json",
			ConfigPresent:        true,
			RuntimePresent:       true,
			ConductorConfigReady: true,
		},
	}
	model := tui.NewModel(services)
	// Navigate to Advanced Setup (index 1 in menu)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	view := model.View()
	if !strings.Contains(view, "Local") || !strings.Contains(view, "Tracked") {
		t.Fatalf("expected storage mode choices, got:\n%s", view)
	}
}

func TestAdvancedSetupTrackedShowsGitignorePrompt(t *testing.T) {
	services := readyAdvancedServices()
	model := tui.NewModel(services)
	// Navigate to Advanced Setup
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	// Select Tracked row
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	view := model.View()
	if !strings.Contains(view, "Agent Priority") {
		t.Fatalf("expected tracked selection to advance, got:\n%s", view)
	}
}

func TestAdvancedSetupTrackedKeepsGitignorePromptInline(t *testing.T) {
	services := readyAdvancedServices()
	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyDown})

	view := model.View()
	if !strings.Contains(view, "Plan Storage Mode") {
		t.Fatalf("expected still on storage screen, got:\n%s", view)
	}
	if !strings.Contains(view, ".gitignore") {
		t.Fatalf("expected inline gitignore prompt, got:\n%s", view)
	}
	if strings.Contains(view, "Agent Priority") {
		t.Fatalf("should not advance yet, got:\n%s", view)
	}
}

func TestAdvancedSetupTrackedEnterAdvancesAfterInlineChoice(t *testing.T) {
	services := readyAdvancedServices()
	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	if !strings.Contains(view, "Agent Priority") {
		t.Fatalf("expected next step after inline tracked confirm, got:\n%s", view)
	}
}

func TestAdvancedSetupAgentPriorityReorder(t *testing.T) {
	services := &fakeServices{
		setup: tui.SetupStatus{
			WorkingDir:           "/tmp/demo",
			ProjectRoot:          "/tmp/demo",
			ConfigPath:           "/tmp/demo/springfield.toml",
			RuntimeDir:           "/tmp/demo/.springfield",
			ConductorConfigPath:  "/tmp/demo/.springfield/conductor/config.json",
			ConfigPresent:        true,
			RuntimePresent:       true,
			ConductorConfigReady: true,
		},
		agentDetections: []tui.AgentDetection{
			{ID: "claude", Name: "Claude Code", Installed: true},
			{ID: "codex", Name: "Codex CLI", Installed: false},
			{ID: "gemini", Name: "Gemini CLI", Installed: true},
		},
	}

	model := tui.NewModel(services)
	// Navigate to Advanced Setup (index 1)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	// Pick Local storage (Enter on default)
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	if !strings.Contains(view, "claude") || !strings.Contains(view, "codex") || !strings.Contains(view, "gemini") {
		t.Fatalf("expected agent priority list, got:\n%s", view)
	}
	if !strings.Contains(view, "installed") {
		t.Fatalf("expected install status, got:\n%s", view)
	}

	// Reorder: move claude down with j
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	view = model.View()
	codexIdx := strings.Index(view, "codex")
	claudeIdx := strings.Index(view, "claude")
	if codexIdx > claudeIdx {
		t.Fatalf("expected codex above claude after reorder, got:\n%s", view)
	}
}

func TestAdvancedSetupSettingsForm(t *testing.T) {
	services := &fakeServices{
		setup: tui.SetupStatus{
			WorkingDir:           "/tmp/demo",
			ProjectRoot:          "/tmp/demo",
			ConfigPath:           "/tmp/demo/springfield.toml",
			RuntimeDir:           "/tmp/demo/.springfield",
			ConductorConfigPath:  "/tmp/demo/.springfield/conductor/config.json",
			ConfigPresent:        true,
			RuntimePresent:       true,
			ConductorConfigReady: true,
		},
		agentDetections: []tui.AgentDetection{
			{ID: "claude", Name: "Claude Code", Installed: true},
		},
	}
	model := tui.NewModel(services)
	// Advanced Setup (index 1)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	// Local storage (Enter)
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	// Agent priority (Enter to confirm)
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	for _, marker := range []string{"Worktree base", "Max retries", "Ralph iterations", "Ralph timeout"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected settings form to contain %q, got:\n%s", marker, view)
		}
	}
	if !strings.Contains(view, ".worktrees") {
		t.Fatalf("expected default worktree base, got:\n%s", view)
	}
}

func TestAdvancedSetupCompleteStepSavesAndOffersDoctor(t *testing.T) {
	services := &fakeServices{
		setup: tui.SetupStatus{
			WorkingDir:           "/tmp/demo",
			ProjectRoot:          "/tmp/demo",
			ConfigPath:           "/tmp/demo/springfield.toml",
			RuntimeDir:           "/tmp/demo/.springfield",
			ConductorConfigPath:  "/tmp/demo/.springfield/conductor/config.json",
			ConfigPresent:        true,
			RuntimePresent:       true,
			ConductorConfigReady: true,
		},
		agentDetections: []tui.AgentDetection{
			{ID: "claude", Name: "Claude Code", Installed: true},
		},
	}
	model := tui.NewModel(services)
	// Advanced Setup (index 1)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	// Local storage
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	// Agent priority confirm
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	// Settings form — 'c' to confirm
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})

	view := model.View()
	// Should show doctor handoff
	if !strings.Contains(view, "doctor") && !strings.Contains(view, "Doctor") {
		t.Fatalf("expected doctor handoff prompt, got:\n%s", view)
	}
	if services.savePriorityCalls != 1 {
		t.Fatalf("expected SaveAgentPriority called once, got %d", services.savePriorityCalls)
	}
}

func TestAdvancedSetupCompleteShowsInstallStatusSummary(t *testing.T) {
	services := &fakeServices{
		setup: readySetupStatus(),
		agentDetections: []tui.AgentDetection{
			{ID: "claude", Name: "Claude Code", Installed: true},
			{ID: "codex", Name: "Codex CLI", Installed: false},
		},
	}
	model := advanceToAdvancedComplete(t, services)
	view := model.View()
	if !strings.Contains(view, "claude (installed)") {
		t.Fatalf("expected installed status in summary, got:\n%s", view)
	}
	if !strings.Contains(view, "codex (not installed)") {
		t.Fatalf("expected missing status in summary, got:\n%s", view)
	}
}

func TestAdvancedSetupStorageChangeShowsExistingPlansNote(t *testing.T) {
	services := &fakeServices{
		setup: readySetupStatus(),
		agentDetections: []tui.AgentDetection{{ID: "claude", Name: "Claude Code", Installed: true}},
		conductorCurrentCfg: &tui.ConductorCurrentConfig{
			PlansDir: ".springfield/conductor/plans",
			WorktreeBase: ".worktrees",
			MaxRetries: 2,
			RalphIterations: 50,
			RalphTimeout: 3600,
		},
	}
	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})

	view := model.View()
	if !strings.Contains(view, "Existing plans remain at .springfield/conductor/plans") {
		t.Fatalf("expected old plans dir note, got:\n%s", view)
	}
}

func TestSetupShowsBasicAdvancedChoice(t *testing.T) {
	services := &fakeServices{
		setup: tui.SetupStatus{
			WorkingDir:          "/tmp/demo",
			ProjectRoot:         "/tmp/demo",
			ConfigPath:          "/tmp/demo/springfield.toml",
			RuntimeDir:          "/tmp/demo/.springfield",
			ConductorConfigPath: "/tmp/demo/.springfield/conductor/config.json",
			ConfigPresent:       true,
			RuntimePresent:      true,
		},
		conductorSetup: tui.ConductorSetupResult{Created: true},
	}
	model := tui.NewModel(services)
	// Navigate to Guided Setup (first menu item)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	view := model.View()
	if !strings.Contains(view, "Basic") || !strings.Contains(view, "Advanced") {
		t.Fatalf("expected Basic/Advanced choice, got:\n%s", view)
	}
}

func TestSetupBasicUsesDefaultsAndOffersDoctorHandoff(t *testing.T) {
	services := &fakeServices{
		setup: tui.SetupStatus{
			WorkingDir:          "/tmp/demo",
			ProjectRoot:         "/tmp/demo",
			ConfigPath:          "/tmp/demo/springfield.toml",
			RuntimeDir:          "/tmp/demo/.springfield",
			ConductorConfigPath: "/tmp/demo/.springfield/conductor/config.json",
			ConfigPresent:       true,
			RuntimePresent:      true,
		},
		conductorSetup:     tui.ConductorSetupResult{Created: true},
		agentDetections:    []tui.AgentDetection{{ID: "claude", Name: "Claude Code", Installed: true}},
		agentPriorityOrder: []string{"claude"},
	}
	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	// Select Basic (first option)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	view := model.View()
	if services.conductorSetupCalls != 1 {
		t.Fatalf("expected conductor setup called, got %d", services.conductorSetupCalls)
	}
	if !strings.Contains(view, "doctor") && !strings.Contains(view, "Doctor") {
		t.Fatalf("expected doctor handoff, got:\n%s", view)
	}
	if !strings.Contains(view, "claude (installed)") {
		t.Fatalf("expected priority install status in basic summary, got:\n%s", view)
	}
}

func TestSetupAdvancedNavigatesToAdvancedScreen(t *testing.T) {
	services := &fakeServices{
		setup: tui.SetupStatus{
			WorkingDir:          "/tmp/demo",
			ProjectRoot:         "/tmp/demo",
			ConfigPath:          "/tmp/demo/springfield.toml",
			RuntimeDir:          "/tmp/demo/.springfield",
			ConductorConfigPath: "/tmp/demo/.springfield/conductor/config.json",
			ConfigPresent:       true,
			RuntimePresent:      true,
		},
		agentDetections: []tui.AgentDetection{
			{ID: "claude", Name: "Claude Code", Installed: true},
		},
	}
	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	// Down to Advanced, Enter
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	view := model.View()
	if !strings.Contains(view, "Advanced Setup") {
		t.Fatalf("expected Advanced Setup screen, got:\n%s", view)
	}
}

func TestModelRendersDoctorSummary(t *testing.T) {
	model := tui.NewModel(&fakeServices{
		report: doctor.Report{
			Summary: "1/2 agent(s) available. Springfield can operate with the available agent(s).",
			Checks: []doctor.Check{
				{Name: "Claude Code", Binary: "claude", Status: doctor.StatusHealthy},
				{Name: "Codex", Binary: "codex", Status: doctor.StatusMissing, Guidance: "Install Codex CLI"},
			},
		},
	})

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	for _, marker := range []string{"Doctor", "Claude Code", "Install Codex CLI", "1/2 agent(s) available"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected Doctor view to contain %q, got:\n%s", marker, view)
		}
	}
}

func readySetupStatus() tui.SetupStatus {
	return tui.SetupStatus{
		WorkingDir:          "/tmp/demo",
		ProjectRoot:         "/tmp/demo",
		ConfigPath:          "/tmp/demo/springfield.toml",
		RuntimeDir:          "/tmp/demo/.springfield",
		ConductorConfigPath: "/tmp/demo/.springfield/conductor/config.json",
		ConfigPresent:       true,
		RuntimePresent:      true,
		ConductorConfigReady: true,
	}
}

func readyAdvancedServices() *fakeServices {
	return &fakeServices{
		setup: readySetupStatus(),
		agentDetections: []tui.AgentDetection{
			{ID: "claude", Name: "Claude Code", Installed: true},
			{ID: "codex", Name: "Codex CLI", Installed: false},
		},
	}
}

func advanceToAdvancedComplete(t *testing.T, services *fakeServices) tui.Model {
	t.Helper()
	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	return model
}
