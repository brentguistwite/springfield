package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"springfield/internal/features/doctor"
)

type menuItem struct {
	label  string
	screen Screen
	quit   bool
}

var homeMenu = []menuItem{
	{label: "Guided Setup", screen: ScreenSetup},
	{label: "Ralph", screen: ScreenRalph},
	{label: "Conductor", screen: ScreenConductor},
	{label: "Doctor", screen: ScreenDoctor},
	{label: "Quit", quit: true},
}

type homeScreen struct {
	cursor int
}

func newHomeScreen() homeScreen {
	return homeScreen{}
}

func (h homeScreen) Update(msg tea.Msg) (homeScreen, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.KeyMsg:
		switch typed.Type {
		case tea.KeyUp, tea.KeyShiftTab:
			if h.cursor > 0 {
				h.cursor--
			}
		case tea.KeyDown, tea.KeyTab:
			if h.cursor < len(homeMenu)-1 {
				h.cursor++
			}
		case tea.KeyEnter:
			item := homeMenu[h.cursor]
			if item.quit {
				return h, tea.Quit
			}
			return h, navigate(item.screen)
		}

		switch typed.String() {
		case "q":
			return h, tea.Quit
		}
	}

	return h, nil
}

func (h homeScreen) View() string {
	var builder strings.Builder

	builder.WriteString("Springfield\n")
	builder.WriteString("Local-first shell for Ralph and Conductor.\n\n")

	for index, item := range homeMenu {
		cursor := "  "
		if index == h.cursor {
			cursor = "> "
		}
		fmt.Fprintf(&builder, "%s%s\n", cursor, item.label)
	}

	builder.WriteString("\nUp/Down navigate, Enter select, q quit\n")
	return builder.String()
}

type setupScreen struct {
	services   Services
	status     SetupStatus
	lastResult *setupResult
}

type setupResult struct {
	configCreated          bool
	runtimeCreated         bool
	conductorConfigCreated bool
	conductorConfigReused  bool
	err                    string
}

func newSetupScreen(services Services) setupScreen {
	return setupScreen{
		services: services,
		status:   services.SetupStatus(),
	}
}

func (s setupScreen) Update(msg tea.Msg) (setupScreen, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.KeyMsg:
		switch typed.Type {
		case tea.KeyEsc:
			return s, goBack
		case tea.KeyEnter:
			if s.status.NeedsInit() {
				result, err := s.services.InitProject()
				s.lastResult = &setupResult{
					configCreated:  result.ConfigCreated,
					runtimeCreated: result.RuntimeDirCreated,
				}
				if err != nil {
					s.lastResult.err = err.Error()
				}
				s.status = s.services.SetupStatus()
				return s, nil
			}
			if !s.status.ConductorConfigReady {
				result, err := s.services.SetupConductor()
				s.lastResult = &setupResult{
					conductorConfigCreated: result.Created,
					conductorConfigReused:  result.Reused,
				}
				if err != nil {
					s.lastResult.err = err.Error()
				}
				s.status = s.services.SetupStatus()
				return s, nil
			}
			return s, goBack
		}

		switch typed.String() {
		case "r":
			s.status = s.services.SetupStatus()
			return s, nil
		}
	}

	return s, nil
}

func (s setupScreen) View() string {
	var builder strings.Builder

	builder.WriteString("Guided Setup\n\n")

	if s.status.Error != "" {
		fmt.Fprintf(&builder, "Setup error: %s\n\n", s.status.Error)
		builder.WriteString("Esc back\n")
		return builder.String()
	}

	fmt.Fprintf(&builder, "Working dir: %s\n", s.status.WorkingDir)
	fmt.Fprintf(&builder, "Project root: %s\n", s.status.ProjectRoot)
	fmt.Fprintf(&builder, "Config: %s\n", readyLabel(s.status.ConfigPresent, s.status.ConfigPath))
	fmt.Fprintf(&builder, "Runtime dir: %s\n", readyLabel(s.status.RuntimePresent, s.status.RuntimeDir))
	fmt.Fprintf(&builder, "Conductor config: %s\n", readyLabel(s.status.ConductorConfigReady, s.status.ConductorConfigPath))

	if s.lastResult != nil {
		builder.WriteString("\nLast action:\n")
		switch {
		case s.lastResult.err != "":
			fmt.Fprintf(&builder, "  failed: %s\n", s.lastResult.err)
		case s.lastResult.conductorConfigCreated:
			builder.WriteString("  conductor config created\n")
		case s.lastResult.conductorConfigReused:
			builder.WriteString("  conductor config already exists, reused\n")
		default:
			fmt.Fprintf(&builder, "  springfield.toml created: %t\n", s.lastResult.configCreated)
			fmt.Fprintf(&builder, "  .springfield created: %t\n", s.lastResult.runtimeCreated)
		}
	}

	builder.WriteString("\n")
	if s.status.NeedsInit() {
		builder.WriteString("Enter creates springfield.toml and .springfield in the project root.\n")
	} else if !s.status.ConductorConfigReady {
		builder.WriteString("Enter generates conductor config so you can run plans without editing JSON.\n")
	} else {
		builder.WriteString("Core setup is ready. Ralph and Conductor surfaces can use the local project state.\n")
	}
	builder.WriteString("r refresh, Esc back\n")

	return builder.String()
}

type ralphScreen struct {
	services   Services
	summary    RalphSummary
	planCursor int
	lastRun    *RalphRunResult
	lastErr    string
}

func newRalphScreen(services Services) ralphScreen {
	return ralphScreen{
		services: services,
		summary:  services.RalphSummary(),
	}
}

func (r ralphScreen) Update(msg tea.Msg) (ralphScreen, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return r, nil
	}

	switch key.Type {
	case tea.KeyEsc:
		return r, goBack
	case tea.KeyUp, tea.KeyShiftTab:
		if r.planCursor > 0 {
			r.planCursor--
		}
	case tea.KeyDown, tea.KeyTab:
		if r.planCursor < len(r.summary.Plans)-1 {
			r.planCursor++
		}
	}

	if key.String() == "r" && len(r.summary.Plans) > 0 {
		plan := r.summary.Plans[r.planCursor]
		result, err := r.services.RunRalphNext(plan.Name)
		if err != nil {
			r.lastRun = nil
			r.lastErr = err.Error()
		} else {
			r.lastRun = &result
			r.lastErr = ""
		}
		r.summary = r.services.RalphSummary()
	}

	return r, nil
}

func (r ralphScreen) View() string {
	var builder strings.Builder

	builder.WriteString("Ralph\n\n")
	if !r.summary.Ready {
		fmt.Fprintf(&builder, "%s\n\n", r.summary.Reason)
		builder.WriteString("Esc back\n")
		return builder.String()
	}

	if len(r.summary.Plans) == 0 {
		builder.WriteString("No Ralph plans yet.\n")
		builder.WriteString("Use `springfield ralph init --name <plan> --spec <file>` to seed one.\n")
	} else {
		builder.WriteString("Plans:\n")
		for index, plan := range r.summary.Plans {
			cursor := "  "
			if index == r.planCursor {
				cursor = "> "
			}
			fmt.Fprintf(&builder, "%s%s (%d stories, next %s: %s)\n", cursor, plan.Name, plan.StoryCount, plan.NextStoryID, plan.NextStoryTitle)
		}
	}

	builder.WriteString("\nRecent runs:\n")
	if len(r.summary.RecentRuns) == 0 {
		builder.WriteString("  none\n")
	} else {
		for _, run := range r.summary.RecentRuns {
			fmt.Fprintf(&builder, "  - %s / %s [%s]\n", run.PlanName, run.StoryID, run.Status)
		}
	}

	if r.lastRun != nil {
		fmt.Fprintf(&builder, "\nLast run: %s / %s [%s]\n", r.lastRun.PlanName, r.lastRun.StoryID, r.lastRun.Status)
		if r.lastRun.Error != "" {
			fmt.Fprintf(&builder, "  error: %s\n", r.lastRun.Error)
		}
	}
	if r.lastErr != "" {
		fmt.Fprintf(&builder, "\nRun failed: %s\n", r.lastErr)
	}

	builder.WriteString("\nr run next story, Esc back\n")
	return builder.String()
}

type conductorScreen struct {
	services Services
	summary  ConductorSummary
	lastRun  *ConductorRunResult
	lastErr  string
}

func newConductorScreen(services Services) conductorScreen {
	return conductorScreen{
		services: services,
		summary:  services.ConductorSummary(),
	}
}

func (c conductorScreen) Update(msg tea.Msg) (conductorScreen, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return c, nil
	}

	switch key.Type {
	case tea.KeyEsc:
		return c, goBack
	}

	if key.String() == "r" && !c.summary.Done {
		result, err := c.services.RunConductorNext()
		if err != nil {
			c.lastRun = nil
			c.lastErr = err.Error()
		} else {
			c.lastRun = &result
			c.lastErr = ""
		}
		c.summary = c.services.ConductorSummary()
	}

	return c, nil
}

func (c conductorScreen) View() string {
	var builder strings.Builder

	builder.WriteString("Conductor\n\n")
	if !c.summary.Ready {
		fmt.Fprintf(&builder, "%s\n\n", c.summary.Reason)
		builder.WriteString("Esc back\n")
		return builder.String()
	}

	fmt.Fprintf(&builder, "Progress: %d/%d plans completed\n", c.summary.Completed, c.summary.Total)
	if c.summary.Done {
		builder.WriteString("Status: done\n")
	} else {
		builder.WriteString("Status: in progress\n")
	}

	if len(c.summary.Failures) > 0 {
		builder.WriteString("\nFailures:\n")
		for _, failure := range c.summary.Failures {
			fmt.Fprintf(&builder, "  - %s\n", failure)
		}
	}

	if c.summary.NextStep != "" {
		fmt.Fprintf(&builder, "\nNext step: %s\n", c.summary.NextStep)
	}

	if c.lastRun != nil {
		fmt.Fprintf(&builder, "\nLast run: %s\n", strings.Join(c.lastRun.Ran, ", "))
		if c.lastRun.Done {
			builder.WriteString("  all plans completed\n")
		}
	}
	if c.lastErr != "" {
		fmt.Fprintf(&builder, "\nRun failed: %s\n", c.lastErr)
	}

	builder.WriteString("\nr run next phase, Esc back\n")
	return builder.String()
}

type doctorScreen struct {
	report doctor.Report
}

func newDoctorScreen(services Services) doctorScreen {
	return doctorScreen{report: services.DoctorSummary()}
}

func (d doctorScreen) Update(msg tea.Msg) (doctorScreen, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.Type == tea.KeyEsc {
		return d, goBack
	}

	return d, nil
}

func (d doctorScreen) View() string {
	var builder strings.Builder

	builder.WriteString("Doctor\n\n")
	for _, check := range d.report.Checks {
		fmt.Fprintf(&builder, "  %s %s (%s)\n", doctorStatusLabel(check.Status), check.Name, check.Binary)
		if check.Guidance != "" {
			fmt.Fprintf(&builder, "    %s\n", check.Guidance)
		}
	}

	builder.WriteString("\n")
	builder.WriteString(d.report.Summary)
	builder.WriteString("\n\nEsc back\n")
	return builder.String()
}

func navigate(screen Screen) tea.Cmd {
	return func() tea.Msg {
		return NavigateMsg{Screen: screen}
	}
}

func goBack() tea.Msg {
	return BackMsg{}
}

func readyLabel(ready bool, path string) string {
	if ready {
		return "ready at " + path
	}
	return "missing at " + path
}

func doctorStatusLabel(status doctor.CheckStatus) string {
	switch status {
	case doctor.StatusHealthy:
		return "OK"
	case doctor.StatusMissing:
		return "MISS"
	default:
		return "BAD"
	}
}
