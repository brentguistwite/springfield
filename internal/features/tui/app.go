package tui

import (
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// App owns the Springfield TUI shell process surface.
type App struct {
	stdin    *os.File
	stdout   *os.File
	services Services
}

// NewApp builds the Springfield TUI shell with explicit process streams.
func NewApp(stdin, stdout *os.File) *App {
	return &App{
		stdin:    stdin,
		stdout:   stdout,
		services: newRuntimeServices(os.Getwd, exec.LookPath),
	}
}

// Run enters the interactive shell when attached to a terminal.
func (app *App) Run() error {
	model := NewModel(app.services)
	if !app.isInteractive() {
		return app.render(model.View())
	}

	program := tea.NewProgram(
		model,
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

func (app *App) render(view string) error {
	if app.stdout == nil {
		return nil
	}

	_, err := fmt.Fprintln(app.stdout, view)
	return err
}
