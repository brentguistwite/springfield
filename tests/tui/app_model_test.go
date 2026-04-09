package tui_test

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"springfield/internal/core/config"
	"springfield/internal/features/doctor"
	"springfield/internal/features/planner"
	"springfield/internal/features/tui"
)

type fakeServices struct {
	setup                        tui.SetupStatus
	initResult                   config.InitResult
	initErr                      error
	conductorSetup               tui.ConductorSetupResult
	conductorSetupErr            error
	conductorSetupCalls          int
	springfieldStatus            tui.SpringfieldStatus
	springfieldDiagnosis         tui.SpringfieldDiagnosis
	springfieldRunResult         tui.SpringfieldRunResult
	springfieldRunErr            error
	springfieldRunCalls          int
	springfieldRunEvents         []tui.RuntimeEvent
	springfieldResumeResult      tui.SpringfieldRunResult
	springfieldResumeErr         error
	springfieldResumeCalls       int
	springfieldResumeEvents      []tui.RuntimeEvent
	ralph                        tui.RalphSummary
	conductor                    tui.ConductorSummary
	report                       doctor.Report
	initCalls                    int
	ralphRunResult               tui.RalphRunResult
	ralphRunErr                  error
	ralphRunCalls                int
	ralphRunPlan                 string
	ralphRunEvents               []tui.RuntimeEvent
	conductorRunResult           tui.ConductorRunResult
	conductorRunErr              error
	conductorRunCalls            int
	conductorRunEvents           []tui.RuntimeEvent
	agentDetections              []tui.AgentDetection
	agentPriorityOrder           []string
	agentExecutionModes          tui.AgentExecutionModes
	ensureExecutionDefaultsErr   error
	ensureExecutionDefaultsCalls int
	conductorCurrentCfg          *tui.ConductorCurrentConfig
	savePriorityCalls            int
	savePriorityArg              []string
	savePriorityErr              error
	saveExecutionModesCalls      int
	saveExecutionModesArg        tui.SaveAgentExecutionModesInput
	saveExecutionModesErr        error
	updateConductorCalls         int
	updateConductorResult        tui.ConductorSetupResult
	updateConductorErr           error
	planResults                  []tui.PlanWorkResult
	planErr                      error
	planCalls                    int
	planInputs                   []string
	regenerateResults            []tui.PlanWorkResult
	regenerateErr                error
	regenerateCalls              int
	approveCalls                 int
	approveErr                   error
	resetPlanCalls               int
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

func (f *fakeServices) SpringfieldStatus() tui.SpringfieldStatus {
	return f.springfieldStatus
}

func (f *fakeServices) SpringfieldDiagnosis() tui.SpringfieldDiagnosis {
	return f.springfieldDiagnosis
}

func (f *fakeServices) RunSpringfieldWork(onEvent func(tui.RuntimeEvent)) (tui.SpringfieldRunResult, error) {
	f.springfieldRunCalls++
	if onEvent != nil {
		for _, e := range f.springfieldRunEvents {
			onEvent(e)
		}
	}
	return f.springfieldRunResult, f.springfieldRunErr
}

func (f *fakeServices) ResumeSpringfieldWork(onEvent func(tui.RuntimeEvent)) (tui.SpringfieldRunResult, error) {
	f.springfieldResumeCalls++
	if onEvent != nil {
		for _, e := range f.springfieldResumeEvents {
			onEvent(e)
		}
	}
	return f.springfieldResumeResult, f.springfieldResumeErr
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

func (f *fakeServices) AgentExecutionModes() tui.AgentExecutionModes {
	if f.agentExecutionModes.Claude == "" && f.agentExecutionModes.Codex == "" {
		return tui.AgentExecutionModes{
			Claude: "recommended",
			Codex:  "recommended",
		}
	}
	return f.agentExecutionModes
}

func (f *fakeServices) ConductorCurrentConfig() *tui.ConductorCurrentConfig {
	return f.conductorCurrentCfg
}

func (f *fakeServices) SaveAgentPriority(priority []string) error {
	f.savePriorityCalls++
	f.savePriorityArg = priority
	return f.savePriorityErr
}

func (f *fakeServices) SaveAgentExecutionModes(input tui.SaveAgentExecutionModesInput) error {
	f.saveExecutionModesCalls++
	f.saveExecutionModesArg = input
	return f.saveExecutionModesErr
}

func (f *fakeServices) EnsureRecommendedExecutionDefaults() error {
	f.ensureExecutionDefaultsCalls++
	if f.ensureExecutionDefaultsErr != nil {
		return f.ensureExecutionDefaultsErr
	}
	if f.agentExecutionModes.Claude == "" && f.agentExecutionModes.Codex == "" {
		f.agentExecutionModes = tui.AgentExecutionModes{
			Claude: "recommended",
			Codex:  "recommended",
		}
	}
	return nil
}

func (f *fakeServices) UpdateConductor(opts tui.ConductorSetupInput) (tui.ConductorSetupResult, error) {
	f.updateConductorCalls++
	return f.updateConductorResult, f.updateConductorErr
}

func (f *fakeServices) PlanWork(request string) (tui.PlanWorkResult, error) {
	f.planCalls++
	f.planInputs = append(f.planInputs, request)
	if f.planErr != nil {
		return tui.PlanWorkResult{}, f.planErr
	}
	if len(f.planResults) == 0 {
		return tui.PlanWorkResult{}, nil
	}
	result := f.planResults[0]
	if len(f.planResults) > 1 {
		f.planResults = f.planResults[1:]
	}
	return result, nil
}

func (f *fakeServices) RegeneratePlannedWork() (tui.PlanWorkResult, error) {
	f.regenerateCalls++
	if f.regenerateErr != nil {
		return tui.PlanWorkResult{}, f.regenerateErr
	}
	if len(f.regenerateResults) == 0 {
		return tui.PlanWorkResult{}, nil
	}
	result := f.regenerateResults[0]
	if len(f.regenerateResults) > 1 {
		f.regenerateResults = f.regenerateResults[1:]
	}
	return result, nil
}

func (f *fakeServices) ApprovePlannedWork() error {
	f.approveCalls++
	return f.approveErr
}

func (f *fakeServices) ResetPlannedWork() {
	f.resetPlanCalls++
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

func navigateToScreen(t *testing.T, model tui.Model, screen tui.Screen) tui.Model {
	t.Helper()
	return updateModel(t, model, tui.NavigateMsg{Screen: screen})
}

func TestModelStartsOnHomeScreen(t *testing.T) {
	model := tui.NewModel(&fakeServices{})
	view := model.View()

	for _, marker := range []string{"Springfield", "Guided Setup", "New Work", "Status", "Advanced Setup", "Doctor"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected home view to contain %q, got:\n%s", marker, view)
		}
	}
	for _, legacy := range []string{"Ralph", "Conductor"} {
		if strings.Contains(view, legacy) {
			t.Fatalf("expected Springfield-first home view to omit %q, got:\n%s", legacy, view)
		}
	}
}

func TestSpringfieldStatusScreenShowsApprovedWork(t *testing.T) {
	services := &fakeServices{
		springfieldStatus: tui.SpringfieldStatus{
			Ready:  true,
			WorkID: "wave-c2",
			Title:  "Unified execution surface",
			Split:  "single",
			Status: "ready",
			Workstreams: []tui.SpringfieldWorkstreamStatus{
				{Name: "01", Title: "Execution adapter", Status: "ready"},
			},
		},
	}

	model := tui.NewModel(services)
	model = navigateToScreen(t, model, tui.ScreenStatus)

	view := model.View()
	for _, marker := range []string{"Status", "Work: wave-c2", "Unified execution surface", "Status: ready", "01  ready  Execution adapter"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected status view to contain %q, got:\n%s", marker, view)
		}
	}
}

func TestSpringfieldRunStartsApprovedWork(t *testing.T) {
	services := &fakeServices{
		springfieldStatus: tui.SpringfieldStatus{
			Ready:  true,
			WorkID: "wave-c2",
			Title:  "Unified execution surface",
			Split:  "single",
			Status: "ready",
			Workstreams: []tui.SpringfieldWorkstreamStatus{
				{Name: "01", Title: "Execution adapter", Status: "ready"},
			},
		},
	}

	model := tui.NewModel(services)
	model = navigateToScreen(t, model, tui.ScreenStatus)
	if !strings.Contains(model.View(), "r run work") {
		t.Fatalf("expected run hint in Springfield status view, got:\n%s", model.View())
	}
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})

	services.springfieldStatus.Status = "completed"
	services.springfieldStatus.Workstreams[0].Status = "completed"
	model = sendMsg(t, model, tui.SpringfieldRunCompleteMsg{
		Result: tui.SpringfieldRunResult{WorkID: "wave-c2", Status: "completed"},
	})

	view := model.View()
	for _, marker := range []string{"Status: completed", "Last run: wave-c2 [completed]"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected completed Springfield run view to contain %q, got:\n%s", marker, view)
		}
	}
}

func TestSpringfieldDiagnoseShowsFailureGuidance(t *testing.T) {
	services := &fakeServices{
		springfieldStatus: tui.SpringfieldStatus{
			Ready:  true,
			WorkID: "wave-c2",
			Title:  "Unified execution surface",
			Split:  "multi",
			Status: "failed",
			Workstreams: []tui.SpringfieldWorkstreamStatus{
				{Name: "01", Title: "CLI surface", Status: "completed"},
				{Name: "02", Title: "TUI surface", Status: "failed", Error: "agent failed", EvidencePath: ".springfield/work/wave-c2/logs/02.log"},
			},
		},
		springfieldDiagnosis: tui.SpringfieldDiagnosis{
			WorkID:   "wave-c2",
			Status:   "failed",
			NextStep: "Review the failing workstreams, then resume the work.",
			Failures: []tui.SpringfieldDiagnosisFailure{
				{Workstream: "02", Title: "TUI surface", Error: "agent failed", EvidencePath: ".springfield/work/wave-c2/logs/02.log"},
			},
		},
	}

	model := tui.NewModel(services)
	model = navigateToScreen(t, model, tui.ScreenStatus)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})

	view := model.View()
	for _, marker := range []string{"Status — Diagnose", "02  TUI surface", "Evidence: .springfield/work/wave-c2/logs/02.log", "resume"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected diagnosis view to contain %q, got:\n%s", marker, view)
		}
	}
}

func TestSpringfieldResumeRunsFailedWork(t *testing.T) {
	services := &fakeServices{
		springfieldStatus: tui.SpringfieldStatus{
			Ready:  true,
			WorkID: "wave-c2",
			Title:  "Unified execution surface",
			Split:  "single",
			Status: "failed",
			Workstreams: []tui.SpringfieldWorkstreamStatus{
				{Name: "01", Title: "Execution adapter", Status: "failed", Error: "agent failed"},
			},
		},
		springfieldDiagnosis: tui.SpringfieldDiagnosis{
			WorkID:   "wave-c2",
			Status:   "failed",
			NextStep: "Review the failing workstreams, then resume the work.",
			Failures: []tui.SpringfieldDiagnosisFailure{
				{Workstream: "01", Title: "Execution adapter", Error: "agent failed"},
			},
		},
	}

	model := tui.NewModel(services)
	model = navigateToScreen(t, model, tui.ScreenStatus)
	if !strings.Contains(model.View(), "r resume work") {
		t.Fatalf("expected resume hint in Springfield status view, got:\n%s", model.View())
	}
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
}

func plannedDraftResult(id, title, summary string, split planner.Split, workstreams []tui.PlannedWorkstreamSummary) tui.PlanWorkResult {
	return tui.PlanWorkResult{
		Draft: &tui.PlannedWorkDraft{
			WorkID:      id,
			Title:       title,
			Summary:     summary,
			Split:       split,
			Workstreams: workstreams,
		},
	}
}

func TestModelSpringfieldPlanningFlowShowsPlannerQuestion(t *testing.T) {
	services := &fakeServices{
		planResults: []tui.PlanWorkResult{
			{Question: "Which Springfield surface should ship first?"},
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	for _, r := range "Plan Wave B" {
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	if services.planCalls != 1 {
		t.Fatalf("expected one planning call, got %d", services.planCalls)
	}
	if got, want := services.planInputs[0], "Plan Wave B"; got != want {
		t.Fatalf("plan input = %q, want %q", got, want)
	}

	view := model.View()
	for _, marker := range []string{"Planner question:", "Which Springfield surface should ship first?"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected planning view to contain %q, got:\n%s", marker, view)
		}
	}
}

func TestModelSpringfieldPlanningFlowAnswersPlannerQuestionToDraft(t *testing.T) {
	services := &fakeServices{
		planResults: []tui.PlanWorkResult{
			{Question: "Which Springfield surface should ship first?"},
			plannedDraftResult(
				"wave-c1",
				"Wave C1 planning loop",
				"Connect the TUI planning flow to the real planner session.",
				planner.SplitSingle,
				[]tui.PlannedWorkstreamSummary{
					{Name: "01", Title: "Implement Wave C1", Summary: "Keep it in one stream."},
				},
			),
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	for _, r := range "Plan Wave C1" {
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	for _, r := range "Start with New Work" {
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	if got, want := services.planCalls, 2; got != want {
		t.Fatalf("plan calls = %d, want %d", got, want)
	}
	if got, want := services.planInputs[1], "Start with New Work"; got != want {
		t.Fatalf("second plan input = %q, want %q", got, want)
	}

	view := model.View()
	for _, marker := range []string{"Wave C1 planning loop", "Split: single", "Implement Wave C1"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected review marker %q, got:\n%s", marker, view)
		}
	}
}

func TestModelSpringfieldReviewFlowRegenerateRecallsPlanner(t *testing.T) {
	services := &fakeServices{
		planResults: []tui.PlanWorkResult{
			plannedDraftResult(
				"wave-c1",
				"Wave C1 planning loop",
				"Initial draft.",
				planner.SplitSingle,
				[]tui.PlannedWorkstreamSummary{
					{Name: "01", Title: "Initial draft"},
				},
			),
		},
		regenerateResults: []tui.PlanWorkResult{
			plannedDraftResult(
				"wave-c1",
				"Wave C1 planning loop regenerated",
				"Updated draft.",
				planner.SplitMulti,
				[]tui.PlannedWorkstreamSummary{
					{Name: "01", Title: "Planner boundary"},
					{Name: "02", Title: "TUI review flow"},
				},
			),
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})

	if got, want := services.regenerateCalls, 1; got != want {
		t.Fatalf("regenerate calls = %d, want %d", got, want)
	}

	view := model.View()
	for _, marker := range []string{"Wave C1 planning loop regenerated", "Split: multi", "Planner boundary", "Draft regenerated."} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected regenerated review marker %q, got:\n%s", marker, view)
		}
	}
}

func TestModelSpringfieldReviewFlowApprovePersistsDraft(t *testing.T) {
	services := &fakeServices{
		planResults: []tui.PlanWorkResult{
			plannedDraftResult(
				"wave-c1",
				"Wave C1 planning loop",
				"Connect the TUI planning flow to the real planner session.",
				planner.SplitSingle,
				[]tui.PlannedWorkstreamSummary{
					{Name: "01", Title: "Implement Wave C1"},
				},
			),
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})

	if got, want := services.approveCalls, 1; got != want {
		t.Fatalf("approve calls = %d, want %d", got, want)
	}

	view := model.View()
	for _, marker := range []string{"Draft approved and saved under .springfield/work.", "[a] approve", "[g] regenerate", "[b] back"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected review action %q, got:\n%s", marker, view)
		}
	}
}

func TestModelSpringfieldReviewFlowBackReturnsSafely(t *testing.T) {
	services := &fakeServices{
		planResults: []tui.PlanWorkResult{
			plannedDraftResult(
				"wave-c1",
				"Wave C1 planning loop",
				"Connect the TUI planning flow to the real planner session.",
				planner.SplitSingle,
				[]tui.PlannedWorkstreamSummary{
					{Name: "01", Title: "Implement Wave C1"},
				},
			),
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	resetCallsBeforeBack := services.resetPlanCalls
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})

	if got, want := services.resetPlanCalls, resetCallsBeforeBack+1; got != want {
		t.Fatalf("reset calls = %d, want %d", got, want)
	}

	view := model.View()
	for _, marker := range []string{"Describe the work Springfield should plan.", "Request:"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected input view marker %q, got:\n%s", marker, view)
		}
	}
	if strings.Contains(view, "Wave C1 planning loop") {
		t.Fatalf("expected review draft to be cleared after back, got:\n%s", view)
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

	model = navigateToScreen(t, model, tui.ScreenRalph)

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

	model = navigateToScreen(t, model, tui.ScreenConductor)

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
	for _, marker := range []string{"conductor config created", "Setup complete.", "Recommended agent permissions are enabled for Claude and Codex"} {
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
	if !strings.Contains(view, "Setup complete.") {
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
	model = navigateToScreen(t, model, tui.ScreenRalph)

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
	model = navigateToScreen(t, model, tui.ScreenRalph)

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
	model = navigateToScreen(t, model, tui.ScreenRalph)

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
	model = navigateToScreen(t, model, tui.ScreenRalph)

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
	model = navigateToScreen(t, model, tui.ScreenRalph)

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
	model = navigateToScreen(t, model, tui.ScreenRalph)

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
	model = navigateToScreen(t, model, tui.ScreenRalph)

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
	model = navigateToScreen(t, model, tui.ScreenConductor)

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
	model = navigateToScreen(t, model, tui.ScreenConductor)

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
	model = navigateToScreen(t, model, tui.ScreenConductor)

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
	model = navigateToScreen(t, model, tui.ScreenConductor)

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

	model = navigateToScreen(t, model, tui.ScreenRalph)

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

	model = navigateToScreen(t, model, tui.ScreenConductor)

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
	model = navigateToScreen(t, model, tui.ScreenConductor)

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
	model = navigateToScreen(t, model, tui.ScreenConductor)

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
	model = navigateToScreen(t, model, tui.ScreenConductor)

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
	model = navigateToScreen(t, model, tui.ScreenConductor)

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
	model = navigateToScreen(t, model, tui.ScreenConductor)

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
	model = navigateToScreen(t, model, tui.ScreenConductor)

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
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
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
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
	view := model.View()
	if !strings.Contains(view, "Local") || !strings.Contains(view, "Tracked") {
		t.Fatalf("expected storage mode choices, got:\n%s", view)
	}
}

func TestAdvancedSetupTrackedShowsGitignorePrompt(t *testing.T) {
	services := readyAdvancedServices()
	model := tui.NewModel(services)
	// Navigate to Advanced Setup
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
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
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
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
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
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
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
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

func TestAdvancedSetupShowsAgentPermissionsStep(t *testing.T) {
	services := readyAdvancedServices()
	model := tui.NewModel(services)
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	for _, marker := range []string{
		"Agent Permissions",
		"Springfield defaults to running agents without permission prompts.",
		"Claude prompts",
		"Codex prompts",
		"No permission prompts (default)",
	} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected permissions step to contain %q, got:\n%s", marker, view)
		}
	}
}

func TestAdvancedSetupPermissionsShowCustomAndCycle(t *testing.T) {
	services := &fakeServices{
		setup: readySetupStatus(),
		agentDetections: []tui.AgentDetection{
			{ID: "claude", Name: "Claude Code", Installed: true},
			{ID: "codex", Name: "Codex CLI", Installed: true},
		},
		agentExecutionModes: tui.AgentExecutionModes{
			Claude: "custom",
			Codex:  "off",
		},
	}
	model := tui.NewModel(services)
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	if !strings.Contains(view, "Custom") {
		t.Fatalf("expected custom mode visible, got:\n%s", view)
	}
	if !strings.Contains(view, "Ask for permissions") {
		t.Fatalf("expected opt-out label visible, got:\n%s", view)
	}

	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRight})
	view = model.View()
	if !strings.Contains(view, "Claude prompts:      No permission prompts (default)") {
		t.Fatalf("expected first cycle to land on Recommended, got:\n%s", view)
	}

	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRight})
	view = model.View()
	if !strings.Contains(view, "Claude prompts:      Ask for permissions") {
		t.Fatalf("expected second cycle to land on Off, got:\n%s", view)
	}
}

func TestAdvancedSetupPermissionsHelpOmitsHLHint(t *testing.T) {
	services := readyAdvancedServices()
	model := tui.NewModel(services)
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	if strings.Contains(view, "h/l") {
		t.Fatalf("expected help text to omit h/l hint, got:\n%s", view)
	}
	if !strings.Contains(view, "Up/Down select row, Left/Right change, Enter continue, Esc back") {
		t.Fatalf("expected simplified help text, got:\n%s", view)
	}
}

func TestAdvancedSetupPermissionsCopyWrapsToWindowWidth(t *testing.T) {
	services := readyAdvancedServices()
	model := tui.NewModel(services)
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateModel(t, model, tea.WindowSizeMsg{Width: 42, Height: 20})

	view := model.View()
	if !strings.Contains(view, "Springfield defaults to running agents\nwithout permission prompts.") {
		t.Fatalf("expected wrapped permissions copy, got:\n%s", view)
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
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
	// Local storage (Enter)
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	// Agent priority (Enter to confirm)
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	// Agent permissions (Enter to confirm)
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
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
	// Local storage
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	// Agent priority confirm
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	// Agent permissions confirm
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
	if services.saveExecutionModesCalls != 1 {
		t.Fatalf("expected SaveAgentExecutionModes called once, got %d", services.saveExecutionModesCalls)
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
		setup:           readySetupStatus(),
		agentDetections: []tui.AgentDetection{{ID: "claude", Name: "Claude Code", Installed: true}},
		conductorCurrentCfg: &tui.ConductorCurrentConfig{
			PlansDir:        ".springfield/conductor/plans",
			WorktreeBase:    ".worktrees",
			MaxRetries:      2,
			RalphIterations: 50,
			RalphTimeout:    3600,
		},
	}
	model := tui.NewModel(services)
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})

	view := model.View()
	if !strings.Contains(view, "Existing plans remain at .springfield/conductor/plans") {
		t.Fatalf("expected old plans dir note, got:\n%s", view)
	}
}

func TestAdvancedSetupCompleteCallsUpdateConductorWhenConfigExists(t *testing.T) {
	services := &fakeServices{
		setup:           readySetupStatus(),
		agentDetections: []tui.AgentDetection{{ID: "claude", Name: "Claude Code", Installed: true}},
	}
	model := advanceToAdvancedComplete(t, services)
	_ = model
	if services.updateConductorCalls != 1 {
		t.Fatalf("expected UpdateConductor once, got %d", services.updateConductorCalls)
	}
	if services.conductorSetupCalls != 0 {
		t.Fatalf("did not expect SetupConductor, got %d", services.conductorSetupCalls)
	}
}

func TestAdvancedSetupCompleteCallsSetupConductorWhenConfigMissing(t *testing.T) {
	services := &fakeServices{
		setup: tui.SetupStatus{
			WorkingDir:     "/tmp/demo",
			ProjectRoot:    "/tmp/demo",
			ConfigPath:     "/tmp/demo/springfield.toml",
			RuntimeDir:     "/tmp/demo/.springfield",
			ConfigPresent:  true,
			RuntimePresent: true,
		},
		agentDetections: []tui.AgentDetection{{ID: "claude", Name: "Claude Code", Installed: true}},
	}
	model := advanceToAdvancedComplete(t, services)
	_ = model
	if services.conductorSetupCalls != 1 {
		t.Fatalf("expected SetupConductor once, got %d", services.conductorSetupCalls)
	}
	if services.updateConductorCalls != 0 {
		t.Fatalf("did not expect UpdateConductor, got %d", services.updateConductorCalls)
	}
}

func TestAdvancedSetupLastFieldEnterSubmits(t *testing.T) {
	services := &fakeServices{
		setup:           readySetupStatus(),
		agentDetections: []tui.AgentDetection{{ID: "claude", Name: "Claude Code", Installed: true}},
	}
	model := tui.NewModel(services)
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	view := model.View()
	if !strings.Contains(view, "Run doctor") {
		t.Fatalf("expected Enter on last field to submit, got:\n%s", view)
	}
}

func TestAdvancedSetupInvalidNumericFieldShowsError(t *testing.T) {
	services := &fakeServices{
		setup:           readySetupStatus(),
		agentDetections: []tui.AgentDetection{{ID: "claude", Name: "Claude Code", Installed: true}},
	}
	model := tui.NewModel(services)
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyBackspace})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyBackspace})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	view := model.View()
	if !strings.Contains(view, "Max retries must be a whole number") {
		t.Fatalf("expected validation error, got:\n%s", view)
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
	for _, marker := range []string{
		"Basic",
		"Advanced",
		"Springfield-recommended agent permissions",
		"choose storage mode, agent priority, permissions, and tuning options",
	} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected setup choice to contain %q, got:\n%s", marker, view)
		}
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
	if services.ensureExecutionDefaultsCalls != 1 {
		t.Fatalf("expected EnsureRecommendedExecutionDefaults called once, got %d", services.ensureExecutionDefaultsCalls)
	}
	if !strings.Contains(view, "doctor") && !strings.Contains(view, "Doctor") {
		t.Fatalf("expected doctor handoff, got:\n%s", view)
	}
	if !strings.Contains(view, "claude (installed)") {
		t.Fatalf("expected priority install status in basic summary, got:\n%s", view)
	}
	if !strings.Contains(view, "Recommended agent permissions are enabled for Claude and Codex") {
		t.Fatalf("expected recommended permissions note, got:\n%s", view)
	}
}

func TestSetupBasicPreservesTrackedStorageSummary(t *testing.T) {
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
		agentDetections:    []tui.AgentDetection{{ID: "claude", Name: "Claude Code", Installed: true}},
		agentPriorityOrder: []string{"claude"},
		conductorCurrentCfg: &tui.ConductorCurrentConfig{
			PlansDir:        ".conductor/plans",
			WorktreeBase:    ".worktrees",
			MaxRetries:      2,
			RalphIterations: 50,
			RalphTimeout:    3600,
		},
	}

	model := tui.NewModel(services)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	if strings.Contains(view, "Storage: local") {
		t.Fatalf("expected basic summary to avoid wrong local storage label, got:\n%s", view)
	}
	if !strings.Contains(view, "Storage: tracked") {
		t.Fatalf("expected tracked storage summary, got:\n%s", view)
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

	model = navigateToScreen(t, model, tui.ScreenDoctor)

	view := model.View()
	for _, marker := range []string{"Doctor", "Claude Code", "Install Codex CLI", "1/2 agent(s) available"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected Doctor view to contain %q, got:\n%s", marker, view)
		}
	}
}

func readySetupStatus() tui.SetupStatus {
	return tui.SetupStatus{
		WorkingDir:           "/tmp/demo",
		ProjectRoot:          "/tmp/demo",
		ConfigPath:           "/tmp/demo/springfield.toml",
		RuntimeDir:           "/tmp/demo/.springfield",
		ConductorConfigPath:  "/tmp/demo/.springfield/conductor/config.json",
		ConfigPresent:        true,
		RuntimePresent:       true,
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
		agentExecutionModes: tui.AgentExecutionModes{
			Claude: "recommended",
			Codex:  "recommended",
		},
	}
}

func advanceToAdvancedComplete(t *testing.T, services *fakeServices) tui.Model {
	t.Helper()
	model := tui.NewModel(services)
	model = navigateToScreen(t, model, tui.ScreenAdvancedSetup)
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = sendMsg(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	return model
}
