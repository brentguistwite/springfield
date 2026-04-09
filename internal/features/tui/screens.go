package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"springfield/internal/features/doctor"
	"springfield/internal/features/execution"
)

type menuItem struct {
	label  string
	screen Screen
	quit   bool
}

var homeMenu = []menuItem{
	{label: "Guided Setup", screen: ScreenSetup},
	{label: "New Work", screen: ScreenNewWork},
	{label: "Status", screen: ScreenStatus},
	{label: "Advanced Setup", screen: ScreenAdvancedSetup},
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
	builder.WriteString("Local-first shell for planning and running work.\n\n")

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
	executionConfigCreated bool
	executionConfigReused  bool
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
			if !s.status.ExecutionReady {
				defaults := execution.Defaults()
				result, err := s.services.ConfigureExecution(ExecutionConfigInput{
					PlansDir:                   defaults.PlansDir,
					WorktreeBase:               defaults.WorktreeBase,
					MaxRetries:                 defaults.MaxRetries,
					SingleWorkstreamIterations: defaults.SingleWorkstreamIterations,
					SingleWorkstreamTimeout:    defaults.SingleWorkstreamTimeout,
				})
				s.lastResult = &setupResult{
					executionConfigCreated: result.Created,
					executionConfigReused:  result.Reused,
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
	fmt.Fprintf(&b, "Execution config: %s\n", readyLabel(s.status.ExecutionReady, s.status.ExecutionConfigPath))

	if s.lastResult != nil {
		b.WriteString("\nLast action:\n")
		switch {
		case s.lastResult.err != "":
			fmt.Fprintf(&b, "  failed: %s\n", s.lastResult.err)
		case s.lastResult.executionConfigCreated:
			b.WriteString("  execution config created\n")
		case s.lastResult.executionConfigReused:
			b.WriteString("  execution config already exists, reused\n")
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
		b.WriteString("How would you like to configure execution?\n\n")
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
		b.WriteString(fmt.Sprintf("Storage: %s\n", basicSetupStorageLabel(s.services.CurrentExecutionConfig())))
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
			b.WriteString("Recommended agent permissions are enabled for Claude and Codex. Springfield is designed to use these settings so guided planning and execution can run as intended.\n")
		} else {
			b.WriteString("Springfield is designed to use recommended agent permissions for Claude and Codex so guided planning and execution can run as intended.\n")
		}
		b.WriteString("Run doctor to verify prerequisites? [Enter] or [Esc] to go home\n")
	}

	b.WriteString("\nr refresh, Esc back\n")
	return b.String()
}

func basicSetupStorageLabel(current *ExecutionConfig) string {
	if current != nil && execution.IsTrackedPlansDir(current.PlansDir) {
		return "tracked"
	}
	return "local"
}

type newWorkPhase int

const (
	newWorkPhaseInput newWorkPhase = iota
	newWorkPhaseReview
)

type newWorkScreen struct {
	services Services
	phase    newWorkPhase
	input    string
	request  string
	draft    *PlannedWorkDraft
	status   string
	lastErr  string
}

func newNewWorkScreen(services Services) newWorkScreen {
	services.ResetPlannedWork()
	return newWorkScreen{services: services}
}

func (n newWorkScreen) Update(msg tea.Msg) (newWorkScreen, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return n, nil
	}

	if key.Type == tea.KeyEsc {
		n.services.ResetPlannedWork()
		return n, goBack
	}

	switch n.phase {
	case newWorkPhaseInput:
		switch key.Type {
		case tea.KeyBackspace:
			if len(n.input) > 0 {
				n.input = n.input[:len(n.input)-1]
			}
		case tea.KeyEnter:
			request := strings.TrimSpace(n.input)
			if request == "" {
				n.lastErr = "Enter a work request first."
				return n, nil
			}

			result, err := n.services.PlanWork(request)
			if err != nil {
				n.lastErr = err.Error()
				return n, nil
			}

			if result.Question != "" {
				n.request = request
				n.status = "Planner question: " + result.Question
				n.input = ""
				n.lastErr = ""
				return n, nil
			}

			n.phase = newWorkPhaseReview
			if n.request == "" {
				n.request = request
			}
			n.draft = result.Draft
			n.status = ""
			n.lastErr = ""
			n.input = ""
		case tea.KeyRunes:
			n.input += string(key.Runes)
		}

	case newWorkPhaseReview:
		switch key.String() {
		case "a":
			if n.draft == nil {
				return n, nil
			}
			if err := n.services.ApprovePlannedWork(); err != nil {
				n.lastErr = err.Error()
				n.status = ""
				return n, nil
			}
			n.status = "Draft approved and saved under .springfield/work."
			n.lastErr = ""
		case "g":
			result, err := n.services.RegeneratePlannedWork()
			if err != nil {
				n.lastErr = err.Error()
				n.status = ""
				return n, nil
			}
			if result.Question != "" {
				n.phase = newWorkPhaseInput
				n.draft = nil
				n.status = "Planner question: " + result.Question
				n.input = ""
				n.lastErr = ""
				return n, nil
			}
			n.draft = result.Draft
			n.status = "Draft regenerated."
			n.lastErr = ""
		case "b":
			n.phase = newWorkPhaseInput
			n.services.ResetPlannedWork()
			n.draft = nil
			n.request = ""
			n.input = ""
			n.status = ""
			n.lastErr = ""
		}
	}

	return n, nil
}

func (n newWorkScreen) View() string {
	var builder strings.Builder

	builder.WriteString("New Work\n\n")

	switch n.phase {
	case newWorkPhaseInput:
		builder.WriteString("Describe the work Springfield should plan.\n\n")
		builder.WriteString("Request:\n")
		fmt.Fprintf(&builder, "> %s\n", n.input)
		if n.status != "" {
			fmt.Fprintf(&builder, "\n%s\n", n.status)
		}
		if n.lastErr != "" {
			fmt.Fprintf(&builder, "\nError: %s\n", n.lastErr)
		}
		builder.WriteString("\nEnter generate draft, Esc back\n")
	default:
		builder.WriteString("Review Draft\n\n")
		fmt.Fprintf(&builder, "Request: %s\n", n.request)
		if n.draft != nil {
			fmt.Fprintf(&builder, "Work ID: %s\n", n.draft.WorkID)
			fmt.Fprintf(&builder, "Title: %s\n", n.draft.Title)
			fmt.Fprintf(&builder, "Summary: %s\n", n.draft.Summary)
			fmt.Fprintf(&builder, "Split: %s\n", n.draft.Split)
			builder.WriteString("\nWorkstreams:\n")
			for _, workstream := range n.draft.Workstreams {
				fmt.Fprintf(&builder, "  - [%s] %s", workstream.Name, workstream.Title)
				if workstream.Summary != "" {
					fmt.Fprintf(&builder, " — %s", workstream.Summary)
				}
				builder.WriteString("\n")
			}
		}
		if n.status != "" {
			fmt.Fprintf(&builder, "\n%s\n", n.status)
		}
		if n.lastErr != "" {
			fmt.Fprintf(&builder, "\nError: %s\n", n.lastErr)
		}
		builder.WriteString("\n[a] approve  [g] regenerate  [b] back  Esc home\n")
	}

	return builder.String()
}

type springfieldStatusScreen struct {
	services  Services
	summary   SpringfieldStatus
	diagnosis SpringfieldDiagnosis
	monitor   MonitorState
	events    []RuntimeEvent
	eventCh   <-chan RuntimeEvent
	lastRun   *SpringfieldRunResult
	lastErr   string
	diagnose  bool
}

func newSpringfieldStatusScreen(services Services) springfieldStatusScreen {
	return springfieldStatusScreen{
		services:  services,
		summary:   services.SpringfieldStatus(),
		diagnosis: services.SpringfieldDiagnosis(),
	}
}

func (s springfieldStatusScreen) Update(msg tea.Msg) (springfieldStatusScreen, tea.Cmd) {
	switch typed := msg.(type) {
	case RuntimeEventMsg:
		s.events = append(s.events, typed.Event)
		if s.eventCh != nil {
			return s, waitForRuntimeEvent(s.eventCh)
		}
		return s, nil

	case SpringfieldRunCompleteMsg:
		s.eventCh = nil
		s.lastRun = &typed.Result
		if typed.Err != nil {
			s.monitor = MonitorFailed
			s.lastErr = typed.Err.Error()
		} else {
			s.lastErr = ""
			if typed.Result.Status == "completed" {
				s.monitor = MonitorSucceeded
			} else {
				s.monitor = MonitorFailed
			}
		}
		s.summary = s.services.SpringfieldStatus()
		s.diagnosis = s.services.SpringfieldDiagnosis()
		return s, nil

	case tea.KeyMsg:
		switch typed.Type {
		case tea.KeyEsc:
			if s.monitor == MonitorRunning {
				return s, nil
			}
			return s, goBack
		}

		switch typed.String() {
		case "d":
			if s.monitor != MonitorRunning && springfieldHasFailures(s.summary) {
				s.diagnose = !s.diagnose
			}
			return s, nil
		case "r":
			if s.monitor == MonitorRunning || !s.summary.Ready {
				return s, nil
			}
			ch := make(chan RuntimeEvent, 100)
			s.monitor = MonitorRunning
			s.events = nil
			s.lastRun = nil
			s.lastErr = ""
			s.eventCh = ch
			if springfieldShouldResume(s.summary) {
				return s, tea.Batch(
					waitForRuntimeEvent(ch),
					resumeSpringfieldAsync(s.services, ch),
				)
			}
			return s, tea.Batch(
				waitForRuntimeEvent(ch),
				runSpringfieldAsync(s.services, ch),
			)
		}
	}

	return s, nil
}

func (s springfieldStatusScreen) View() string {
	if s.diagnose {
		return s.diagnosisView()
	}

	var builder strings.Builder
	builder.WriteString("Status\n\n")
	if !s.summary.Ready {
		fmt.Fprintf(&builder, "%s\n\nEsc back\n", s.summary.Reason)
		return builder.String()
	}

	fmt.Fprintf(&builder, "Work: %s\n", s.summary.WorkID)
	fmt.Fprintf(&builder, "Title: %s\n", s.summary.Title)
	fmt.Fprintf(&builder, "Split: %s\n", s.summary.Split)
	fmt.Fprintf(&builder, "Status: %s\n", s.summary.Status)

	builder.WriteString("\nWorkstreams:\n")
	for _, workstream := range s.summary.Workstreams {
		fmt.Fprintf(&builder, "  %s  %s  %s\n", workstream.Name, workstream.Status, workstream.Title)
		if workstream.Error != "" {
			fmt.Fprintf(&builder, "    Error: %s\n", workstream.Error)
		}
		if workstream.EvidencePath != "" {
			fmt.Fprintf(&builder, "    Evidence: %s\n", workstream.EvidencePath)
		}
	}

	switch s.monitor {
	case MonitorRunning:
		builder.WriteString("\nStatus: running...\n")
	case MonitorSucceeded:
		builder.WriteString("\nStatus: succeeded\n")
	case MonitorFailed:
		builder.WriteString("\nStatus: failed\n")
	}

	if len(s.events) > 0 {
		builder.WriteString("\nEvents:\n")
		start := 0
		if len(s.events) > 10 {
			start = len(s.events) - 10
		}
		for _, event := range s.events[start:] {
			fmt.Fprintf(&builder, "  [%s] %s\n", event.Source, event.Data)
		}
	}

	if s.monitor != MonitorRunning {
		if s.lastRun != nil {
			fmt.Fprintf(&builder, "\nLast run: %s [%s]\n", s.lastRun.WorkID, s.lastRun.Status)
			if s.lastRun.Error != "" {
				fmt.Fprintf(&builder, "  error: %s\n", s.lastRun.Error)
			}
		}
		if s.lastErr != "" {
			fmt.Fprintf(&builder, "\nRun failed: %s\n", s.lastErr)
		}
	}

	if s.monitor == MonitorRunning {
		builder.WriteString("\nrunning... Esc blocked\n")
		return builder.String()
	}

	action := "r run work"
	if springfieldShouldResume(s.summary) {
		action = "r resume work"
	}
	if springfieldHasFailures(s.summary) {
		fmt.Fprintf(&builder, "\n%s, d diagnose, Esc back\n", action)
		return builder.String()
	}
	fmt.Fprintf(&builder, "\n%s, Esc back\n", action)
	return builder.String()
}

func (s springfieldStatusScreen) diagnosisView() string {
	var builder strings.Builder
	builder.WriteString("Status — Diagnose\n\n")
	fmt.Fprintf(&builder, "Work: %s\n", s.diagnosis.WorkID)
	fmt.Fprintf(&builder, "Status: %s\n", s.diagnosis.Status)
	if s.diagnosis.Summary != "" {
		fmt.Fprintf(&builder, "Summary: %s\n", s.diagnosis.Summary)
	}
	if len(s.diagnosis.FailingWorkstreams) > 0 {
		fmt.Fprintf(&builder, "Failing workstreams: %s\n", strings.Join(s.diagnosis.FailingWorkstreams, ", "))
	}
	if s.diagnosis.LastError != "" {
		fmt.Fprintf(&builder, "Last error: %s\n", s.diagnosis.LastError)
	}
	if s.diagnosis.EvidencePath != "" {
		fmt.Fprintf(&builder, "Evidence: %s\n", s.diagnosis.EvidencePath)
	}
	builder.WriteString("\n")

	if len(s.diagnosis.Failures) == 0 {
		builder.WriteString("No failures detected.\n")
	} else {
		for _, failure := range s.diagnosis.Failures {
			fmt.Fprintf(&builder, "%s  %s\n", failure.Workstream, failure.Title)
			fmt.Fprintf(&builder, "  Error: %s\n", failure.Error)
			if failure.EvidencePath != "" {
				fmt.Fprintf(&builder, "  Evidence: %s\n", failure.EvidencePath)
			}
			builder.WriteString("\n")
		}
	}

	fmt.Fprintf(&builder, "Next step: %s\n", s.diagnosis.NextStep)
	if springfieldShouldResume(s.summary) {
		builder.WriteString("d back, r resume work, Esc back\n")
	} else {
		builder.WriteString("d back, r run work, Esc back\n")
	}
	return builder.String()
}

func springfieldHasFailures(summary SpringfieldStatus) bool {
	for _, workstream := range summary.Workstreams {
		if workstream.Status == "failed" {
			return true
		}
	}
	return false
}

func springfieldShouldResume(summary SpringfieldStatus) bool {
	return summary.Status == "failed" || springfieldHasFailures(summary)
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

func runSpringfieldAsync(svc Services, ch chan<- RuntimeEvent) tea.Cmd {
	return func() tea.Msg {
		result, err := svc.RunSpringfieldWork(func(e RuntimeEvent) {
			ch <- e
		})
		close(ch)
		return SpringfieldRunCompleteMsg{Result: result, Err: err}
	}
}

func resumeSpringfieldAsync(svc Services, ch chan<- RuntimeEvent) tea.Cmd {
	return func() tea.Msg {
		result, err := svc.ResumeSpringfieldWork(func(e RuntimeEvent) {
			ch <- e
		})
		close(ch)
		return SpringfieldRunCompleteMsg{Result: result, Err: err}
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
