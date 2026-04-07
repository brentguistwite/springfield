package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type advancedSetupScreen struct {
	services Services
	status   SetupStatus
}

func newAdvancedSetupScreen(services Services) advancedSetupScreen {
	return advancedSetupScreen{
		services: services,
		status:   services.SetupStatus(),
	}
}

func (a advancedSetupScreen) Update(msg tea.Msg) (advancedSetupScreen, tea.Cmd) {
	// Redirect to guided setup if project not initialized
	if a.status.NeedsInit() {
		return a, navigate(ScreenSetup)
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		if key.Type == tea.KeyEsc {
			return a, goBack
		}
	}

	return a, nil
}

func (a advancedSetupScreen) View() string {
	var builder strings.Builder
	builder.WriteString("Advanced Setup\n\n")
	builder.WriteString("Coming soon — storage mode, agent priority, and conductor settings.\n")
	builder.WriteString("\nEsc back\n")
	return builder.String()
}
