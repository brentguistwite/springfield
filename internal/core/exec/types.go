package exec

import (
	"context"
	"time"
)

// Command describes a subprocess to run.
//
// Env contains environment variable overrides. Entries are MERGED over
// os.Environ() so the adapter need only specify the keys it wants to add
// or override, inheriting PATH/HOME/auth-related vars unchanged.
type Command struct {
	Name    string
	Args    []string
	Stdin   string // written to the process's stdin when non-empty
	Dir     string
	Env     map[string]string
	Timeout time.Duration // zero means no timeout
}

// EventType distinguishes stdout from stderr output.
type EventType string

const (
	EventStdout EventType = "stdout"
	EventStderr EventType = "stderr"
)

// Event is a single line of output from a running process.
type Event struct {
	Type EventType
	Data string
	Time time.Time
}

// EventHandler receives streaming events during execution.
type EventHandler func(Event)

// Result is the outcome of a completed (or failed) process.
type Result struct {
	ExitCode int
	Events   []Event
	Err      error
}

// CommandFunc is the signature for running commands, injectable for testing.
type CommandFunc func(ctx context.Context, cmd Command, handler EventHandler) Result
