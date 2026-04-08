package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"springfield/internal/features/conductor"
	"springfield/internal/features/doctor"
)

type menuItem struct {
	label  string
	screen Screen
	quit   bool
}

var homeMenu = []menuItem{
	{label: "Guided Setup", screen: ScreenSetup},
	{label: "Advanced Setup", screen: ScreenAdvancedSetup},
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

type setupPhase int

const (
	setupPhaseInit setupPhase = iota
	setupPhaseChoice
	setupPhaseBasicDone
)

type setupScreen struct {
	services     Services
	status       SetupStatus
	lastResult   *setupResult
	phase        setupPhase
	choiceCursor int // 0=Basic, 1=Advanced
}

type setupResult struct {
	configCreated          bool
	runtimeCreated         bool
	conductorConfigCreated bool
	conductorConfigReused  bool
	err                    string
}

func newSetupScreen(services Services) setupScreen {
	s := setupScreen{
		services: services,
		status:   services.SetupStatus(),
	}
	if s.status.NeedsInit() {
		s.phase = setupPhaseInit
	} else {
		s.phase = setupPhaseChoice
	}
	return s
}

func (s setupScreen) Update(msg tea.Msg) (setupScreen, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}

	if key.Type == tea.KeyEsc {
		return s, goBack
	}

	if key.String() == "r" {
		s.status = s.services.SetupStatus()
		if s.status.NeedsInit() {
			s.phase = setupPhaseInit
		} else if s.phase == setupPhaseInit {
			s.phase = setupPhaseChoice
		}
		return s, nil
	}

	switch s.phase {
	case setupPhaseInit:
		if key.Type == tea.KeyEnter {
			result, err := s.services.InitProject()
			s.lastResult = &setupResult{
				configCreated:  result.ConfigCreated,
				runtimeCreated: result.RuntimeDirCreated,
			}
			if err != nil {
				s.lastResult.err = err.Error()
			}
			s.status = s.services.SetupStatus()
			if !s.status.NeedsInit() {
				s.phase = setupPhaseChoice
			}
		}

	case setupPhaseChoice:
		switch key.Type {
		case tea.KeyUp, tea.KeyShiftTab:
			if s.choiceCursor > 0 {
				s.choiceCursor--
			}
		case tea.KeyDown, tea.KeyTab:
			if s.choiceCursor < 1 {
				s.choiceCursor++
			}
		case tea.KeyEnter:
			if s.choiceCursor == 1 {
				// Advanced -> navigate to AdvancedSetup screen
				return s, navigate(ScreenAdvancedSetup)
			}
			if err := s.services.EnsureRecommendedExecutionDefaults(); err != nil {
				s.lastResult = &setupResult{err: err.Error()}
				s.phase = setupPhaseBasicDone
				return s, nil
			}
			// Basic path -- setup conductor with defaults
			if !s.status.ConductorConfigReady {
				defaults := conductor.SetupDefaults()
				result, err := s.services.SetupConductor(ConductorSetupInput{
					PlansDir:        defaults.PlansDir,
					WorktreeBase:    defaults.WorktreeBase,
					MaxRetries:      defaults.MaxRetries,
					RalphIterations: defaults.RalphIterations,
					RalphTimeout:    defaults.RalphTimeout,
				})
				s.lastResult = &setupResult{
					conductorConfigCreated: result.Created,
					conductorConfigReused:  result.Reused,
				}
				if err != nil {
					s.lastResult.err = err.Error()
				}
				s.status = s.services.SetupStatus()
			}
			s.phase = setupPhaseBasicDone
		}

	case setupPhaseBasicDone:
		if key.Type == tea.KeyEnter {
			return s, navigate(ScreenDoctor)
		}
	}

	return s, nil
}

func (s setupScreen) View() string {
	var b strings.Builder
	b.WriteString("Guided Setup\n\n")

	if s.status.Error != "" {
		fmt.Fprintf(&b, "Setup error: %s\n\nEsc back\n", s.status.Error)
		return b.String()
	}

	// Always show status
	fmt.Fprintf(&b, "Working dir: %s\n", s.status.WorkingDir)
	fmt.Fprintf(&b, "Project root: %s\n", s.status.ProjectRoot)
	fmt.Fprintf(&b, "Config: %s\n", readyLabel(s.status.ConfigPresent, s.status.ConfigPath))
	fmt.Fprintf(&b, "Runtime dir: %s\n", readyLabel(s.status.RuntimePresent, s.status.RuntimeDir))
	fmt.Fprintf(&b, "Conductor config: %s\n", readyLabel(s.status.ConductorConfigReady, s.status.ConductorConfigPath))

	if s.lastResult != nil {
		b.WriteString("\nLast action:\n")
		switch {
		case s.lastResult.err != "":
			fmt.Fprintf(&b, "  failed: %s\n", s.lastResult.err)
		case s.lastResult.conductorConfigCreated:
			b.WriteString("  conductor config created\n")
		case s.lastResult.conductorConfigReused:
			b.WriteString("  conductor config already exists, reused\n")
		default:
			fmt.Fprintf(&b, "  springfield.toml created: %t\n", s.lastResult.configCreated)
			fmt.Fprintf(&b, "  .springfield created: %t\n", s.lastResult.runtimeCreated)
		}
	}

	b.WriteString("\n")
	switch s.phase {
	case setupPhaseInit:
		b.WriteString("Enter creates springfield.toml and .springfield in the project root.\n")
	case setupPhaseChoice:
		b.WriteString("How would you like to configure conductor?\n\n")
		choices := []struct{ label, desc string }{
			{"Basic", "use local storage and Springfield-recommended agent permissions (recommended)"},
			{"Advanced", "choose storage mode, agent priority, permissions, and tuning options"},
		}
		for i, c := range choices {
			cursor := "  "
			if i == s.choiceCursor {
				cursor = "> "
			}
			fmt.Fprintf(&b, "%s%s — %s\n", cursor, c.label, c.desc)
		}
	case setupPhaseBasicDone:
		b.WriteString("Setup complete.\n")
		b.WriteString(fmt.Sprintf("Storage: %s\n", basicSetupStorageLabel(s.services.ConductorCurrentConfig())))
		priority := s.services.AgentPriority()
		agents := sortAgentsByPriority(s.services.DetectAgents(), priority)
		b.WriteString("Agent priority:\n")
		for _, agent := range agents {
			status := "not installed"
			if agent.Installed {
				status = "installed"
			}
			fmt.Fprintf(&b, "  - %s (%s)\n", agent.ID, status)
		}
		modes := s.services.AgentExecutionModes()
		if modes.Claude == "recommended" && modes.Codex == "recommended" {
			b.WriteString("Recommended agent permissions are enabled for Claude and Codex. Springfield is designed to use these settings so Ralph and Conductor can run as intended.\n")
		} else {
			b.WriteString("Springfield is designed to use recommended agent permissions for Claude and Codex so Ralph and Conductor can run as intended.\n")
		}
		b.WriteString("Run doctor to verify prerequisites? [Enter] or [Esc] to go home\n")
	}

	b.WriteString("\nr refresh, Esc back\n")
	return b.String()
}

func basicSetupStorageLabel(current *ConductorCurrentConfig) string {
	if current != nil && current.PlansDir == conductor.TrackedPlansDir {
		return "tracked"
	}
	return "local"
}

type ralphScreen struct {
	services   Services
	summary    RalphSummary
	planCursor int
	monitor    MonitorState
	events     []RuntimeEvent
	eventCh    <-chan RuntimeEvent
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
	switch typed := msg.(type) {
	case RuntimeEventMsg:
		r.events = append(r.events, typed.Event)
		if r.eventCh != nil {
			return r, waitForRuntimeEvent(r.eventCh)
		}
		return r, nil

	case RalphRunCompleteMsg:
		r.eventCh = nil
		if typed.Err != nil {
			r.monitor = MonitorFailed
			r.lastErr = typed.Err.Error()
			r.lastRun = nil
		} else {
			r.lastRun = &typed.Result
			r.lastErr = ""
			if typed.Result.Status == "passed" {
				r.monitor = MonitorSucceeded
			} else {
				r.monitor = MonitorFailed
			}
		}
		r.summary = r.services.RalphSummary()
		return r, nil

	case tea.KeyMsg:
		switch typed.Type {
		case tea.KeyEsc:
			if r.monitor == MonitorRunning {
				return r, nil
			}
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

		if typed.String() == "r" && r.monitor != MonitorRunning && len(r.summary.Plans) > 0 {
			plan := r.summary.Plans[r.planCursor]
			ch := make(chan RuntimeEvent, 100)
			r.monitor = MonitorRunning
			r.events = nil
			r.lastRun = nil
			r.lastErr = ""
			r.eventCh = ch
			return r, tea.Batch(
				waitForRuntimeEvent(ch),
				runRalphAsync(r.services, plan.Name, ch),
			)
		}
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

	switch r.monitor {
	case MonitorRunning:
		builder.WriteString("\nStatus: running...\n")
	case MonitorSucceeded:
		builder.WriteString("\nStatus: succeeded\n")
	case MonitorFailed:
		builder.WriteString("\nStatus: failed\n")
	}

	if len(r.events) > 0 {
		builder.WriteString("\nEvents:\n")
		start := 0
		if len(r.events) > 10 {
			start = len(r.events) - 10
		}
		for _, event := range r.events[start:] {
			fmt.Fprintf(&builder, "  [%s] %s\n", event.Source, event.Data)
		}
	}

	if r.monitor != MonitorRunning {
		if r.lastRun != nil {
			fmt.Fprintf(&builder, "\nLast run: %s / %s [%s]\n", r.lastRun.PlanName, r.lastRun.StoryID, r.lastRun.Status)
			if r.lastRun.Error != "" {
				fmt.Fprintf(&builder, "  error: %s\n", r.lastRun.Error)
			}
		}
		if r.lastErr != "" {
			fmt.Fprintf(&builder, "\nRun failed: %s\n", r.lastErr)
		}
	}

	if r.monitor == MonitorRunning {
		builder.WriteString("\nrunning... Esc blocked\n")
	} else {
		builder.WriteString("\nr run next story, Esc back\n")
	}
	return builder.String()
}

type conductorScreen struct {
	services Services
	summary  ConductorSummary
	monitor  MonitorState
	events   []RuntimeEvent
	eventCh  <-chan RuntimeEvent
	lastRun  *ConductorRunResult
	lastErr  string
	diagnose bool
}

func newConductorScreen(services Services) conductorScreen {
	return conductorScreen{
		services: services,
		summary:  services.ConductorSummary(),
	}
}

func (c conductorScreen) Update(msg tea.Msg) (conductorScreen, tea.Cmd) {
	switch typed := msg.(type) {
	case RuntimeEventMsg:
		c.events = append(c.events, typed.Event)
		if c.eventCh != nil {
			return c, waitForRuntimeEvent(c.eventCh)
		}
		return c, nil

	case ConductorRunCompleteMsg:
		c.eventCh = nil
		if typed.Err != nil {
			c.monitor = MonitorFailed
			c.lastErr = typed.Err.Error()
			c.lastRun = nil
		} else {
			c.lastRun = &typed.Result
			c.lastErr = ""
			if typed.Result.Error != "" {
				c.monitor = MonitorFailed
			} else {
				c.monitor = MonitorSucceeded
			}
		}
		c.summary = c.services.ConductorSummary()
		return c, nil

	case tea.KeyMsg:
		switch typed.Type {
		case tea.KeyEsc:
			if c.monitor == MonitorRunning {
				return c, nil
			}
			return c, goBack
		}

		switch typed.String() {
		case "d":
			if c.monitor != MonitorRunning {
				c.diagnose = !c.diagnose
			}
			return c, nil
		}

		if typed.String() == "r" && c.monitor != MonitorRunning && !c.summary.Done {
			ch := make(chan RuntimeEvent, 100)
			c.monitor = MonitorRunning
			c.events = nil
			c.lastRun = nil
			c.lastErr = ""
			c.eventCh = ch
			return c, tea.Batch(
				waitForRuntimeEvent(ch),
				runConductorAsync(c.services, ch),
			)
		}
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

	if c.diagnose {
		return c.diagnosisView()
	}

	fmt.Fprintf(&builder, "Progress: %d/%d plans completed\n", c.summary.Completed, c.summary.Total)
	if c.summary.Done {
		builder.WriteString("Status: done\n")
	} else if c.monitor == MonitorRunning {
		builder.WriteString("Status: running...\n")
	} else if c.monitor == MonitorFailed {
		builder.WriteString("Status: failed\n")
	} else if c.monitor == MonitorSucceeded {
		builder.WriteString("Status: succeeded\n")
	} else {
		builder.WriteString("Status: in progress\n")
	}

	if len(c.summary.Failures) > 0 {
		builder.WriteString("\nFailures:\n")
		for _, f := range c.summary.Failures {
			fmt.Fprintf(&builder, "  - %s: %s\n", f.Plan, f.Error)
		}
	}

	if len(c.events) > 0 {
		builder.WriteString("\nEvents:\n")
		start := 0
		if len(c.events) > 10 {
			start = len(c.events) - 10
		}
		for _, event := range c.events[start:] {
			fmt.Fprintf(&builder, "  [%s] %s\n", event.Source, event.Data)
		}
	}

	if c.monitor != MonitorRunning {
		if c.lastRun != nil {
			fmt.Fprintf(&builder, "\nLast run: %s\n", strings.Join(c.lastRun.Ran, ", "))
			if c.lastRun.Done {
				builder.WriteString("  all plans completed\n")
			}
			if c.lastRun.Error != "" {
				fmt.Fprintf(&builder, "  error: %s\n", c.lastRun.Error)
			}
		}
		if c.lastErr != "" {
			fmt.Fprintf(&builder, "\nRun failed: %s\n", c.lastErr)
		}
	}

	if c.monitor == MonitorRunning {
		builder.WriteString("\nrunning... Esc blocked\n")
	} else {
		hints := "r run next phase"
		if len(c.summary.Failures) > 0 {
			hints += ", d diagnose"
		}
		fmt.Fprintf(&builder, "\n%s, Esc back\n", hints)
	}
	return builder.String()
}

func (c conductorScreen) diagnosisView() string {
	var builder strings.Builder

	builder.WriteString("Conductor — Diagnosis\n\n")
	fmt.Fprintf(&builder, "Progress: %d/%d plans completed\n\n", c.summary.Completed, c.summary.Total)

	if len(c.summary.Failures) == 0 {
		builder.WriteString("No failures detected.\n")
	} else {
		for _, f := range c.summary.Failures {
			fmt.Fprintf(&builder, "Plan: %s\n", f.Plan)
			fmt.Fprintf(&builder, "  Error: %s\n", f.Error)
			if f.Agent != "" {
				fmt.Fprintf(&builder, "  Agent: %s\n", f.Agent)
			}
			fmt.Fprintf(&builder, "  Attempts: %d\n", f.Attempts)
			if f.EvidencePath != "" {
				fmt.Fprintf(&builder, "  Evidence: %s\n", f.EvidencePath)
			}
			builder.WriteString("\n")
		}
	}

	builder.WriteString("d back, r run next phase, Esc back\n")
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

func waitForRuntimeEvent(ch <-chan RuntimeEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return nil
		}
		return RuntimeEventMsg{Event: event}
	}
}

func runRalphAsync(svc Services, planName string, ch chan<- RuntimeEvent) tea.Cmd {
	return func() tea.Msg {
		result, err := svc.RunRalphNext(planName, func(e RuntimeEvent) {
			ch <- e
		})
		close(ch)
		return RalphRunCompleteMsg{Result: result, Err: err}
	}
}

func runConductorAsync(svc Services, ch chan<- RuntimeEvent) tea.Cmd {
	return func() tea.Msg {
		result, err := svc.RunConductorNext(func(e RuntimeEvent) {
			ch <- e
		})
		close(ch)
		return ConductorRunCompleteMsg{Result: result, Err: err}
	}
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
