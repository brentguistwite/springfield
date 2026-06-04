package runtime

import (
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/exec"
)

// Request describes what the runtime should execute.
type Request struct {
	AgentIDs          []agents.ID
	Prompt            string
	WorkDir           string
	Timeout           time.Duration
	OnEvent           exec.EventHandler
	ExecutionSettings agents.ExecutionSettings

	// MaxTurnsPerIteration caps the agent turns a single Run may consume
	// before the runtime synthesizes a retryable [TurnCapExceededReason]
	// failure. Zero (the default) disables the cap so callers that don't opt
	// in keep their prior behavior. Callers that DO opt in pass
	// config.Config.MaxTurnsPerIteration() — see internal/core/config.
	//
	// The cap exists because the installed claude CLI exposes no --max-turns
	// flag; Springfield watches the stream-json result event's num_turns and
	// trips its own circuit-breaker. Enforcing in the runtime layer (not the
	// caller) lets the synthesized failure flow through the standard
	// ClassifyError → cooldown chain so an over-cap iteration falls through
	// to the next agent in [AgentIDs] instead of failing the whole run.
	MaxTurnsPerIteration int

	// WorkCompleteCheck, when non-nil and the cap is enabled, is called by
	// the runtime after a successful agent run to ask "did the agent's work
	// legitimately complete the iteration?". When it returns true, the turn
	// cap is defused — a 200-turn run that genuinely finished the work is
	// not a thrash. When it returns false (or this field is nil), an
	// over-cap run is synthesized into a retryable failure.
	//
	// Lives as a callback because the "is work complete" predicate is the
	// caller's domain knowledge (e.g. planrun: every user story passes AND
	// COMPLETE was emitted) — runtime is intentionally ignorant of PRD or
	// plan semantics.
	//
	// CONTRACT: implementations of the AgentRunner interface MUST invoke
	// this callback synchronously, before Run returns. Callers commonly
	// close over mutable state (e.g. planrun captures currentPRD by
	// reference and mutates it after Run returns); an async invocation
	// would race with those mutations. The production [Runner] honors
	// this. Fake runners used in tests must do the same — see
	// internal/features/conductor/planrun/runner_iteration_test.go for the
	// pattern.
	WorkCompleteCheck func(events []exec.Event) bool

	// ReviewerRole marks this as an independent-reviewer run, which is
	// legitimately tool-free (it reasons over an inline diff with tools
	// forbidden). The runner passes requireToolAction = !ReviewerRole to
	// ValidateResult, so a reviewer's clean tool-free transcript is accepted
	// and the verdict scanner — not the tool-action contract — judges it.
	// Zero value (false) keeps the strict implementer contract; only
	// planreview.Review sets it true.
	ReviewerRole bool
}

// Status is the outcome of a runtime execution.
type Status string

const (
	StatusPassed Status = "passed"
	StatusFailed Status = "failed"
)

// Result is the outcome of a runtime execution.
type Result struct {
	Agent     agents.ID
	Status    Status
	ExitCode  int
	Events    []exec.Event
	Err       error
	StartedAt time.Time
	EndedAt   time.Time
}
