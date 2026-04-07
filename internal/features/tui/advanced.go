package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"springfield/internal/features/conductor"
)

type advancedStep int

const (
	stepStorageMode advancedStep = iota
	stepGitignoreConfirm
	stepAgentPriority
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

	// Gitignore confirm
	gitignoreCursor int // 0=Yes, 1=No
}

func newAdvancedSetupScreen(services Services) advancedSetupScreen {
	return advancedSetupScreen{
		services: services,
		status:   services.SetupStatus(),
		step:     stepStorageMode,
		plansDir: conductor.LocalPlansDir,
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
	case stepGitignoreConfirm:
		return a.updateGitignoreConfirm(key)
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
			a.step = stepAgentPriority
		} else {
			a.plansDir = conductor.TrackedPlansDir
			a.step = stepGitignoreConfirm
		}
	}
	return a, nil
}

func (a advancedSetupScreen) updateGitignoreConfirm(key tea.KeyMsg) (advancedSetupScreen, tea.Cmd) {
	switch key.Type {
	case tea.KeyUp, tea.KeyShiftTab:
		if a.gitignoreCursor > 0 {
			a.gitignoreCursor--
		}
	case tea.KeyDown, tea.KeyTab:
		if a.gitignoreCursor < 1 {
			a.gitignoreCursor++
		}
	case tea.KeyEnter:
		a.updateGitignore = (a.gitignoreCursor == 0)
		a.step = stepAgentPriority
	}
	return a, nil
}

func (a advancedSetupScreen) View() string {
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
		b.WriteString("\nUp/Down navigate, Enter select, Esc back\n")

	case stepGitignoreConfirm:
		b.WriteString("Update .gitignore?\n\n")
		b.WriteString("Tracked plans need .gitignore entries to avoid committing state files.\n\n")
		gitignoreOpts := []string{"Yes, update .gitignore", "No, I'll handle it manually"}
		for i, opt := range gitignoreOpts {
			cursor := "  "
			if i == a.gitignoreCursor {
				cursor = "> "
			}
			fmt.Fprintf(&b, "%s%s\n", cursor, opt)
		}
		b.WriteString("\nUp/Down navigate, Enter select, Esc back\n")

	case stepAgentPriority:
		b.WriteString("Agent priority step — not yet implemented.\n")
		b.WriteString("\nEsc back\n")

	default:
		b.WriteString("Setup complete.\n")
		b.WriteString("\nEsc back\n")
	}

	return b.String()
}
