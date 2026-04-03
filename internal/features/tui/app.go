package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

const placeholderView = `Springfield TUI Placeholder

Local-first shell for the unified Ralph and Conductor product surface.

Subcommands stay available underneath the TUI:
  springfield ralph
  springfield conductor
  springfield doctor`

// App owns the temporary Springfield TUI startup surface.
type App struct {
	stdin  *os.File
	stdout *os.File
}

// NewApp builds the startup TUI with explicit process streams.
func NewApp(stdin, stdout *os.File) *App {
	return &App{
		stdin:  stdin,
		stdout: stdout,
	}
}

// Run enters the interactive placeholder when attached to a terminal.
func (app *App) Run() error {
	if !app.isInteractive() {
		return app.renderPlaceholder()
	}

	program := tea.NewProgram(
		model{},
		tea.WithInput(app.stdin),
		tea.WithOutput(app.stdout),
	)

	_, err := program.Run()
	return err
}

func (app *App) isInteractive() bool {
	if app.stdin == nil || app.stdout == nil {
		return false
	}

	return term.IsTerminal(int(app.stdin.Fd())) && term.IsTerminal(int(app.stdout.Fd()))
}

func (app *App) renderPlaceholder() error {
	if app.stdout == nil {
		return nil
	}

	_, err := fmt.Fprintln(app.stdout, placeholderView)
	return err
}

type model struct{}

func (model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "enter", "q":
			return m, tea.Quit
		}
	}

	return m, nil
}

func (model) View() string {
	return placeholderView + "\n\nPress enter, q, or ctrl+c to exit.\n"
}
