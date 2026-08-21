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
	// Stall, when non-nil, receives a liveness heartbeat on every event Run
	// emits and watches for event-recency staleness in the background. Run
	// NEVER signals or kills the subprocess based on it — a stall verdict is
	// advisory only; the process still runs to its own completion or the
	// Timeout deadline. Nil disables stall detection.
	Stall StallMonitor
}

// StallMonitor observes a running subprocess's liveness so a caller can classify
// a silent, event-less slice as possibly-wedged well before its wall-clock
// timeout. It is deliberately narrow: Run only feeds it heartbeats and runs its
// watcher — it can never reach back and touch the subprocess.
type StallMonitor interface {
	// Observe records a liveness heartbeat: one event arrived.
	Observe()
	// Watch runs the staleness watcher until ctx is cancelled. Run launches it
	// in a goroutine for the subprocess's lifetime and cancels it on exit.
	Watch(ctx context.Context)
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
