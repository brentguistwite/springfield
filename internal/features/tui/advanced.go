package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"springfield/internal/features/conductor"
)

type advancedStep int

const (
	stepStorageMode advancedStep = iota
	stepAgentPriority
	stepAgentPermissions
	stepSettingsForm
	stepComplete
)

type advancedSetupScreen struct {
	services Services
	status   SetupStatus
	step     advancedStep

	// Storage mode
	storageCursor   int // 0=Local, 1=Tracked
	plansDir        string
	updateGitignore bool
	gitignoreChoice bool

	// Agent priority
	agentList   []AgentDetection
	agentCursor int

	// Agent permissions
	executionCursor int
	claudeMode      string
	codexMode       string

	// Settings form
	formFields  []formField
	formCursor  int
	formEditing bool
	formErr     string

	// Completion
	completed        bool
	completeErr      string
	oldPlansDir      string
	completionAgents []AgentDetection
}

type formField struct {
	label string
	value string
}

func newAdvancedSetupScreen(services Services) advancedSetupScreen {
	status := services.SetupStatus()
	agents := services.DetectAgents()
	if priority := services.AgentPriority(); len(priority) > 0 {
		agents = sortAgentsByPriority(agents, priority)
	}
	s := advancedSetupScreen{
		services:        services,
		status:          status,
		step:            stepStorageMode,
		plansDir:        conductor.LocalPlansDir,
		agentList:       agents,
		formFields:      defaultFormFields(),
		gitignoreChoice: true,
	}
	modes := services.AgentExecutionModes()
	s.claudeMode = normalizeExecutionMode(modes.Claude)
	s.codexMode = normalizeExecutionMode(modes.Codex)
	// On re-entry with existing config, load current storage mode
	if status.ConductorConfigReady {
		current := services.ConductorCurrentConfig()
		if current != nil {
			s.plansDir = current.PlansDir
			s.oldPlansDir = current.PlansDir
			if current.PlansDir == conductor.TrackedPlansDir {
				s.storageCursor = 1
			}
			s.formFields = []formField{
				{label: "Worktree base", value: current.WorktreeBase},
				{label: "Max retries", value: fmt.Sprintf("%d", current.MaxRetries)},
				{label: "Ralph iterations", value: fmt.Sprintf("%d", current.RalphIterations)},
				{label: "Ralph timeout (s)", value: fmt.Sprintf("%d", current.RalphTimeout)},
			}
		}
	}
	return s
}

func defaultFormFields() []formField {
	defaults := conductor.SetupDefaults()
	return []formField{
		{label: "Worktree base", value: defaults.WorktreeBase},
		{label: "Max retries", value: fmt.Sprintf("%d", defaults.MaxRetries)},
		{label: "Ralph iterations", value: fmt.Sprintf("%d", defaults.RalphIterations)},
		{label: "Ralph timeout (s)", value: fmt.Sprintf("%d", defaults.RalphTimeout)},
	}
}

func (a advancedSetupScreen) Update(msg tea.Msg) (advancedSetupScreen, tea.Cmd) {
	if a.status.NeedsInit() {
		return a, navigate(ScreenSetup)
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return a, nil
	}

	if key.Type == tea.KeyEsc {
		return a, goBack
	}

	switch a.step {
	case stepStorageMode:
		return a.updateStorageMode(key)
	case stepAgentPriority:
		return a.updateAgentPriority(key)
	case stepAgentPermissions:
		return a.updateAgentPermissions(key)
	case stepSettingsForm:
		return a.updateSettingsForm(key)
	case stepComplete:
		return a.updateComplete(key)
	}

	return a, nil
}

func (a advancedSetupScreen) updateStorageMode(key tea.KeyMsg) (advancedSetupScreen, tea.Cmd) {
	switch key.Type {
	case tea.KeyUp, tea.KeyShiftTab:
		if a.storageCursor > 0 {
			a.storageCursor--
		}
	case tea.KeyDown, tea.KeyTab:
		if a.storageCursor < 1 {
			a.storageCursor++
		}
	case tea.KeyEnter:
		if a.storageCursor == 0 {
			a.plansDir = conductor.LocalPlansDir
			a.updateGitignore = false
		} else {
			a.plansDir = conductor.TrackedPlansDir
			a.updateGitignore = a.gitignoreChoice
		}
		a.step = stepAgentPriority
	}

	switch key.String() {
	case "y":
		if a.storageCursor == 1 {
			a.gitignoreChoice = true
		}
	case "n":
		if a.storageCursor == 1 {
			a.gitignoreChoice = false
		}
	}
	return a, nil
}

func (a advancedSetupScreen) updateAgentPriority(key tea.KeyMsg) (advancedSetupScreen, tea.Cmd) {
	switch key.Type {
	case tea.KeyUp, tea.KeyShiftTab:
		if a.agentCursor > 0 {
			a.agentCursor--
		}
	case tea.KeyDown, tea.KeyTab:
		if a.agentCursor < len(a.agentList)-1 {
			a.agentCursor++
		}
	case tea.KeyEnter:
		a.step = stepAgentPermissions
		return a, nil
	}

	switch key.String() {
	case "j":
		if a.agentCursor < len(a.agentList)-1 {
			a.agentList[a.agentCursor], a.agentList[a.agentCursor+1] = a.agentList[a.agentCursor+1], a.agentList[a.agentCursor]
			a.agentCursor++
		}
	case "k":
		if a.agentCursor > 0 {
			a.agentList[a.agentCursor], a.agentList[a.agentCursor-1] = a.agentList[a.agentCursor-1], a.agentList[a.agentCursor]
			a.agentCursor--
		}
	}

	return a, nil
}

func (a advancedSetupScreen) updateAgentPermissions(key tea.KeyMsg) (advancedSetupScreen, tea.Cmd) {
	switch key.Type {
	case tea.KeyUp, tea.KeyShiftTab:
		if a.executionCursor > 0 {
			a.executionCursor--
		}
	case tea.KeyDown, tea.KeyTab:
		if a.executionCursor < 1 {
			a.executionCursor++
		}
	case tea.KeyLeft:
		a = a.cycleExecutionMode()
	case tea.KeyRight:
		a = a.cycleExecutionMode()
	case tea.KeyEnter:
		a.step = stepSettingsForm
		return a, nil
	}

	switch key.String() {
	case "k":
		if a.executionCursor > 0 {
			a.executionCursor--
		}
	case "j":
		if a.executionCursor < 1 {
			a.executionCursor++
		}
	case "h", "l":
		a = a.cycleExecutionMode()
	}

	return a, nil
}

func (a advancedSetupScreen) updateSettingsForm(key tea.KeyMsg) (advancedSetupScreen, tea.Cmd) {
	if a.formEditing {
		switch key.Type {
		case tea.KeyEnter, tea.KeyEsc:
			a.formEditing = false
		case tea.KeyBackspace:
			if len(a.formFields[a.formCursor].value) > 0 {
				a.formFields[a.formCursor].value = a.formFields[a.formCursor].value[:len(a.formFields[a.formCursor].value)-1]
			}
		case tea.KeyRunes:
			a.formFields[a.formCursor].value += string(key.Runes)
		}
		return a, nil
	}

	switch key.Type {
	case tea.KeyUp, tea.KeyShiftTab:
		if a.formCursor > 0 {
			a.formCursor--
		}
	case tea.KeyDown, tea.KeyTab:
		if a.formCursor < len(a.formFields)-1 {
			a.formCursor++
		}
	case tea.KeyEnter:
		if a.formCursor == len(a.formFields)-1 {
			return a.submitSettings()
		}
		a.formEditing = true
	}

	switch key.String() {
	case "e":
		a.formEditing = true
	case "c":
		return a.submitSettings()
	}

	return a, nil
}

func normalizeExecutionMode(mode string) string {
	switch mode {
	case "recommended", "off", "custom":
		return mode
	default:
		return "off"
	}
}

func executionModeLabel(mode string) string {
	switch normalizeExecutionMode(mode) {
	case "recommended":
		return "No permission prompts (default)"
	case "custom":
		return "Custom"
	default:
		return "Ask for permissions"
	}
}

func nextExecutionMode(mode string) string {
	switch normalizeExecutionMode(mode) {
	case "custom":
		return "recommended"
	case "recommended":
		return "off"
	default:
		return "recommended"
	}
}

func (a advancedSetupScreen) cycleExecutionMode() advancedSetupScreen {
	if a.executionCursor == 0 {
		a.claudeMode = nextExecutionMode(a.claudeMode)
		return a
	}
	a.codexMode = nextExecutionMode(a.codexMode)
	return a
}

func sortAgentsByPriority(agents []AgentDetection, priority []string) []AgentDetection {
	rank := make(map[string]int, len(priority))
	for i, id := range priority {
		rank[id] = i
	}
	sorted := make([]AgentDetection, len(agents))
	copy(sorted, agents)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, oki := rank[sorted[i].ID]
		rj, okj := rank[sorted[j].ID]
		if oki && okj {
			return ri < rj
		}
		if oki {
			return true
		}
		return false
	})
	return sorted
}

func (a advancedSetupScreen) agentPriorityIDs() []string {
	ids := make([]string, len(a.agentList))
	for i, agent := range a.agentList {
		ids[i] = agent.ID
	}
	return ids
}

func parseWholeNumber(label, raw string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number", label)
	}
	return value, nil
}

func (a advancedSetupScreen) validateSettings() (ConductorSetupInput, error) {
	maxRetries, err := parseWholeNumber("Max retries", a.formFields[1].value)
	if err != nil {
		return ConductorSetupInput{}, err
	}
	iterations, err := parseWholeNumber("Ralph iterations", a.formFields[2].value)
	if err != nil {
		return ConductorSetupInput{}, err
	}
	timeout, err := parseWholeNumber("Ralph timeout (s)", a.formFields[3].value)
	if err != nil {
		return ConductorSetupInput{}, err
	}
	return ConductorSetupInput{
		PlansDir:        a.plansDir,
		WorktreeBase:    a.formFields[0].value,
		MaxRetries:      maxRetries,
		RalphIterations: iterations,
		RalphTimeout:    timeout,
		UpdateGitignore: a.updateGitignore,
	}, nil
}

func (a advancedSetupScreen) submitSettings() (advancedSetupScreen, tea.Cmd) {
	input, err := a.validateSettings()
	if err != nil {
		a.formErr = err.Error()
		return a, nil
	}
	a.formErr = ""
	a.step = stepComplete
	a = a.finalizeWithInput(input)
	return a, nil
}

func (a advancedSetupScreen) finalizeWithInput(input ConductorSetupInput) advancedSetupScreen {
	priority := a.agentPriorityIDs()
	a.completionAgents = append([]AgentDetection(nil), a.agentList...)
	if err := a.services.SaveAgentPriority(priority); err != nil {
		a.completeErr = err.Error()
		a.completed = true
		return a
	}
	if err := a.services.SaveAgentExecutionModes(SaveAgentExecutionModesInput{
		Claude: a.claudeMode,
		Codex:  a.codexMode,
	}); err != nil {
		a.completeErr = err.Error()
		a.completed = true
		return a
	}

	if a.status.ConductorConfigReady {
		_, err := a.services.UpdateConductor(input)
		if err != nil {
			a.completeErr = err.Error()
		}
	} else {
		_, err := a.services.SetupConductor(input)
		if err != nil {
			a.completeErr = err.Error()
		}
	}

	a.completed = true
	return a
}

func (a advancedSetupScreen) updateComplete(key tea.KeyMsg) (advancedSetupScreen, tea.Cmd) {
	if key.Type == tea.KeyEnter {
		return a, navigate(ScreenDoctor)
	}
	return a, nil
}

func yesNo(selected bool, yes bool) string {
	if selected == yes {
		if yes {
			return "Y"
		}
		return "n"
	}
	if yes {
		return "y"
	}
	return "N"
}

func (a advancedSetupScreen) View(width int) string {
	var b strings.Builder
	b.WriteString("Advanced Setup\n\n")

	switch a.step {
	case stepStorageMode:
		b.WriteString("Plan Storage Mode\n\n")
		storageOptions := []struct {
			label string
			desc  string
		}{
			{"Local", fmt.Sprintf("plans in %s, not version-controlled", conductor.LocalPlansDir)},
			{"Tracked", fmt.Sprintf("plans in %s, checked into git", conductor.TrackedPlansDir)},
		}
		for i, opt := range storageOptions {
			cursor := "  "
			if i == a.storageCursor {
				cursor = "> "
			}
			fmt.Fprintf(&b, "%s%s — %s\n", cursor, opt.label, opt.desc)
		}
		if a.storageCursor == 1 {
			fmt.Fprintf(&b, "\nUpdate .gitignore? [%s/%s]\n", yesNo(a.gitignoreChoice, true), yesNo(a.gitignoreChoice, false))
		}
		b.WriteString("\nUp/Down navigate, Enter select, Esc back\n")

	case stepAgentPriority:
		b.WriteString("Agent Priority (top = primary, bottom = last fallback)\n\n")
		for i, agent := range a.agentList {
			cursor := "  "
			if i == a.agentCursor {
				cursor = "> "
			}
			status := "installed"
			if !agent.Installed {
				status = "not installed"
			}
			fmt.Fprintf(&b, "%s%d. %s (%s)\n", cursor, i+1, agent.ID, status)
		}
		b.WriteString("\nUp/Down select, j/k reorder, Enter confirm, Esc back\n")

	case stepAgentPermissions:
		b.WriteString("Agent Permissions\n\n")
		b.WriteString(wrapText("Springfield defaults to running agents without permission prompts.", width))
		b.WriteString("\n")
		b.WriteString(wrapText("Leave this on unless you want the agent to stop and ask before privileged actions. If you opt out, runs may pause for approval or fail at permission boundaries.", width))
		b.WriteString("\n\n")
		rows := []struct {
			label string
			value string
		}{
			{label: "Claude prompts", value: executionModeLabel(a.claudeMode)},
			{label: "Codex prompts", value: executionModeLabel(a.codexMode)},
		}
		for i, row := range rows {
			cursor := "  "
			if i == a.executionCursor {
				cursor = "> "
			}
			fmt.Fprintf(&b, "%s%-20s %s\n", cursor, row.label+":", row.value)
		}
		b.WriteString("\nUp/Down select row, Left/Right change, Enter continue, Esc back\n")

	case stepSettingsForm:
		b.WriteString("Conductor Settings\n\n")
		if a.formErr != "" {
			fmt.Fprintf(&b, "Error: %s\n\n", a.formErr)
		}
		for i, field := range a.formFields {
			cursor := "  "
			if i == a.formCursor {
				if a.formEditing {
					cursor = "* "
				} else {
					cursor = "> "
				}
			}
			fmt.Fprintf(&b, "%s%-20s %s\n", cursor, field.label+":", field.value)
		}
		b.WriteString("\nUp/Down navigate, e edit field, Enter edit, c confirm all, Esc back\n")

	case stepComplete:
		if a.completeErr != "" {
			fmt.Fprintf(&b, "Error: %s\n\n", a.completeErr)
			b.WriteString("Esc back\n")
		} else {
			b.WriteString("Configuration saved.\n\n")
			if a.plansDir == conductor.LocalPlansDir {
				b.WriteString("Storage: local\n")
			} else {
				b.WriteString("Storage: tracked\n")
			}
			b.WriteString("Agent priority:\n")
			for _, agent := range a.completionAgents {
				status := "not installed"
				if agent.Installed {
					status = "installed"
				}
				fmt.Fprintf(&b, "  - %s (%s)\n", agent.ID, status)
			}
			if a.oldPlansDir != "" && a.oldPlansDir != a.plansDir {
				fmt.Fprintf(&b, "\nNote: Existing plans remain at %s\n", a.oldPlansDir)
			}
			b.WriteString("\nRun doctor to verify prerequisites? [Enter] or [Esc] to go home\n")
		}

	default:
		b.WriteString("Setup complete.\n")
		b.WriteString("\nEsc back\n")
	}

	return b.String()
}

func wrapText(text string, width int) string {
	if width <= 0 {
		return text
	}

	lines := strings.Split(text, "\n")
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		wrapped = append(wrapped, wrapLine(line, width))
	}
	return strings.Join(wrapped, "\n")
}

func wrapLine(line string, width int) string {
	if width <= 0 || len(line) <= width {
		return line
	}

	words := strings.Fields(line)
	if len(words) == 0 {
		return ""
	}

	var lines []string
	current := words[0]
	currentLen := len(words[0])

	for _, word := range words[1:] {
		if currentLen+1+len(word) > width {
			lines = append(lines, current)
			current = word
			currentLen = len(word)
			continue
		}
		current += " " + word
		currentLen += 1 + len(word)
	}

	lines = append(lines, current)
	return strings.Join(lines, "\n")
}
