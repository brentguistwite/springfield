package tui

import tea "github.com/charmbracelet/bubbletea"

// Model is the top-level Bubble Tea model for the Springfield TUI shell.
type Model struct {
	services  Services
	screen    Screen
	width     int
	home      homeScreen
	setup     setupScreen
	advanced  advancedSetupScreen
	ralph     ralphScreen
	conductor conductorScreen
	doctor    doctorScreen
}

// NewModel builds a shell model around the provided service boundary.
func NewModel(services Services) Model {
	if services == nil {
		services = newRuntimeServices(nil, nil)
	}

	return Model{
		services: services,
		screen:   ScreenHome,
		home:     newHomeScreen(),
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		if key.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
	}

	switch typed := msg.(type) {
	case NavigateMsg:
		m.screen = typed.Screen
		switch typed.Screen {
		case ScreenSetup:
			m.setup = newSetupScreen(m.services)
		case ScreenAdvancedSetup:
			m.advanced = newAdvancedSetupScreen(m.services)
			if m.advanced.status.NeedsInit() {
				m.screen = ScreenSetup
				m.setup = newSetupScreen(m.services)
				return m, nil
			}
		case ScreenRalph:
			m.ralph = newRalphScreen(m.services)
		case ScreenConductor:
			m.conductor = newConductorScreen(m.services)
		case ScreenDoctor:
			m.doctor = newDoctorScreen(m.services)
		}
		return m, nil
	case BackMsg:
		m.screen = ScreenHome
		return m, nil
	case tea.WindowSizeMsg:
		m.width = typed.Width
	}

	var cmd tea.Cmd
	switch m.screen {
	case ScreenHome:
		m.home, cmd = m.home.Update(msg)
	case ScreenSetup:
		m.setup, cmd = m.setup.Update(msg)
	case ScreenAdvancedSetup:
		m.advanced, cmd = m.advanced.Update(msg)
	case ScreenRalph:
		m.ralph, cmd = m.ralph.Update(msg)
	case ScreenConductor:
		m.conductor, cmd = m.conductor.Update(msg)
	case ScreenDoctor:
		m.doctor, cmd = m.doctor.Update(msg)
	}

	return m, cmd
}

func (m Model) View() string {
	switch m.screen {
	case ScreenSetup:
		return m.setup.View()
	case ScreenAdvancedSetup:
		return m.advanced.View(m.width)
	case ScreenRalph:
		return m.ralph.View()
	case ScreenConductor:
		return m.conductor.View()
	case ScreenDoctor:
		return m.doctor.View()
	default:
		return m.home.View()
	}
}
