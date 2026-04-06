package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"springfield/internal/core/config"
	"springfield/internal/features/doctor"
	"springfield/internal/features/tui"
)

type fakeServices struct {
	setup      tui.SetupStatus
	initResult config.InitResult
	initErr    error
	ralph      tui.RalphSummary
	conductor  tui.ConductorSummary
	report     doctor.Report
	initCalls  int
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

func (f *fakeServices) RalphSummary() tui.RalphSummary {
	return f.ralph
}

func (f *fakeServices) ConductorSummary() tui.ConductorSummary {
	return f.conductor
}

func (f *fakeServices) DoctorSummary() doctor.Report {
	return f.report
}

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

func TestModelStartsOnHomeScreen(t *testing.T) {
	model := tui.NewModel(&fakeServices{})
	view := model.View()

	for _, marker := range []string{"Springfield", "Guided Setup", "Ralph", "Conductor", "Doctor"} {
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
	for _, marker := range []string{"springfield.toml created: true", ".springfield created: true", "Core setup is ready"} {
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
			Failures:  []string{"02-config: compile error"},
			NextStep:  "Fix failures then run: springfield conductor resume",
		},
	})

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	for _, marker := range []string{"Conductor", "Progress: 1/3", "02-config: compile error", "resume"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected Conductor view to contain %q, got:\n%s", marker, view)
		}
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
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	view := model.View()
	for _, marker := range []string{"Doctor", "Claude Code", "Install Codex CLI", "1/2 agent(s) available"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("expected Doctor view to contain %q, got:\n%s", marker, view)
		}
	}
}
