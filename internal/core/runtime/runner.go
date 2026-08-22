package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/exec"
	"springfield/internal/core/stall"
)

// defaultCooldown is the fallback duration installed when a retryable
// failure has no parseable reset time. Long enough to not hot-loop;
// short enough to recover within one workday.
const defaultCooldown = 1 * time.Hour

// Runner resolves an agent and executes a prompt through it. Maintains a
// per-agent cooldown map so a rate-limited agent is skipped on subsequent
// Run calls until its cooldown expires.
type Runner struct {
	registry agents.Registry
	run      exec.CommandFunc
	now      func() time.Time

	mu        sync.Mutex
	cooldowns map[agents.ID]time.Time
}

// NewRunner creates a Runner with production defaults.
func NewRunner(registry agents.Registry) *Runner {
	return &Runner{
		registry:  registry,
		run:       exec.Run,
		now:       time.Now,
		cooldowns: map[agents.ID]time.Time{},
	}
}

// NewTestRunner creates a Runner with injectable command execution and clock.
func NewTestRunner(registry agents.Registry, runFn exec.CommandFunc, clock func() time.Time) *Runner {
	return &Runner{
		registry:  registry,
		run:       runFn,
		now:       clock,
		cooldowns: map[agents.ID]time.Time{},
	}
}

// SetCooldown installs a "do not retry before" timestamp for an agent.
// Production callers should let Run install cooldowns automatically based
// on adapter parsing; exported for tests.
func (r *Runner) SetCooldown(id agents.ID, until time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cooldowns[id] = until
}

// GetCooldown returns the active cooldown timestamp for an agent, or the
// zero time if none is set.
func (r *Runner) GetCooldown(id agents.ID) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cooldowns[id]
}

func (r *Runner) clearCooldown(id agents.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cooldowns, id)
}

func (r *Runner) installCooldown(id agents.ID, until time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cooldowns[id] = until
}

func (r *Runner) inCooldown(id agents.ID, now time.Time) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	until, ok := r.cooldowns[id]
	if !ok {
		return time.Time{}, false
	}
	return until, now.Before(until)
}

// Run resolves the agent, builds a command, and executes it. Honors agent
// cooldowns: if an agent's cooldown is still active, it is skipped and the
// next agent in the chain is tried.
func (r *Runner) Run(ctx context.Context, req Request) Result {
	start := r.now()
	agentIDs := normalizeAgentIDs(req.AgentIDs)
	if len(agentIDs) == 0 {
		return Result{
			Status:    StatusFailed,
			Err:       fmt.Errorf("runtime request missing agent chain"),
			StartedAt: start,
			EndedAt:   r.now(),
		}
	}

	var last Result
	var attempts []Attempt
	allSkipped := true
	for _, agentID := range agentIDs {
		if until, cooled := r.inCooldown(agentID, r.now()); cooled {
			r.emitSkipEvent(req.OnEvent, agentID, until)
			continue
		}
		allSkipped = false
		// Only narrate dispatch when a fallback chain exists; a single-agent
		// run has nothing to fall through to, so the line would be pure noise
		// in its evidence stream.
		if len(agentIDs) > 1 {
			r.emitLog(req.OnEvent, fmt.Sprintf("springfield: dispatching agent %s", agentID))
		}

		resolved, err := r.registry.Resolve(agents.ResolveInput{ProjectDefault: agentID})
		if err != nil {
			return Result{
				Agent:     agentID,
				Status:    StatusFailed,
				Err:       fmt.Errorf("resolve agent: %w", err),
				StartedAt: start,
				EndedAt:   r.now(),
				Attempts:  attempts,
			}
		}

		commander, ok := resolved.Adapter.(agents.Commander)
		if !ok {
			return Result{
				Agent:     agentID,
				Status:    StatusFailed,
				Err:       fmt.Errorf("agent %q does not support command execution", agentID),
				StartedAt: start,
				EndedAt:   r.now(),
				Attempts:  attempts,
			}
		}

		cmd, err := commander.Command(agents.CommandInput{
			Prompt:            req.Prompt,
			WorkDir:           req.WorkDir,
			ExecutionSettings: req.ExecutionSettings,
		})
		if err != nil {
			return Result{
				Agent:     agentID,
				Status:    StatusFailed,
				Err:       fmt.Errorf("build command for %s: %w", agentID, err),
				StartedAt: start,
				EndedAt:   r.now(),
				Attempts:  attempts,
			}
		}
		cmd.Timeout = req.Timeout
		// Event-recency stall detection: a fresh Detector per dispatch (its idle
		// timer is scoped to this subprocess). exec.Run heartbeats it on every
		// event and races its watcher against the wall-clock deadline; a stall
		// verdict fires OnStall for advisory escalation and NEVER kills the proc.
		// Disabled when the threshold is zero (New returns a no-op detector).
		if req.StallThreshold > 0 {
			cmd.Stall = stall.New(req.StallThreshold, r.now, req.OnStall)
		}
		// Inject caller env (e.g. the slice's port block) without clobbering
		// keys the adapter set deliberately — the adapter owns its own control
		// vars, so an existing key wins over the request's.
		if len(req.Env) > 0 {
			if cmd.Env == nil {
				cmd.Env = make(map[string]string, len(req.Env))
			}
			for k, v := range req.Env {
				if _, ok := cmd.Env[k]; !ok {
					cmd.Env[k] = v
				}
			}
		}

		iterStart := r.now()
		execResult := r.run(ctx, cmd, req.OnEvent)
		status := StatusPassed
		if execResult.ExitCode != 0 || execResult.Err != nil {
			status = StatusFailed
		}

		// Turn-cap circuit breaker: runs on any clean exit (exit 0, no
		// process error) BEFORE ValidateResult. Ordering is load-bearing —
		// the dogfood thrash shape is exactly "agent burned 84 turns and
		// emitted a malformed transcript with no successful tool_result".
		// If ValidateResult fires first, it synthesizes
		// "claude exited without a successful tool_result"; that error
		// string carries no rate-limit signal and ClassifyError falls
		// through to Fatal, so the fallback chain never gets a turn at all.
		// Running the cap check first means the turn-cap error wins, gets
		// classified retryable (via the iteration-turn-cap-exceeded needle),
		// and codex/gemini get the iteration. WorkCompleteCheck is the
		// caller's domain-specific defuse — when nil, every over-cap run is
		// treated as thrash.
		if status == StatusPassed && req.MaxTurnsPerIteration > 0 {
			honored := req.WorkCompleteCheck != nil && req.WorkCompleteCheck(execResult.Events)
			if capErr := EnforceTurnCap(execResult.Events, req.MaxTurnsPerIteration, honored); capErr != nil {
				status = StatusFailed
				execResult.Err = capErr
			}
		}

		if status == StatusPassed {
			if validator, ok := commander.(agents.ResultValidator); ok {
				// Zero-value-is-strict: a non-reviewer request leaves
				// ReviewerRole false, so requireToolAction is true (the
				// implementer contract). Only an explicit reviewer run relaxes.
				if err := validator.ValidateResult(execResult, !req.ReviewerRole); err != nil {
					status = StatusFailed
					execResult.Err = err
				}
			}
		}

		attempt := Attempt{
			Agent:     agentID,
			ExitCode:  execResult.ExitCode,
			Events:    execResult.Events,
			Err:       execResult.Err,
			StartedAt: iterStart,
			EndedAt:   r.now(),
		}
		last = Result{
			Agent:     agentID,
			Status:    status,
			ExitCode:  execResult.ExitCode,
			Events:    execResult.Events,
			Err:       execResult.Err,
			StartedAt: start,
			EndedAt:   attempt.EndedAt,
		}
		if status == StatusPassed {
			attempts = append(attempts, attempt)
			last.Attempts = attempts
			r.clearCooldown(agentID)
			return last
		}
		class := agents.ErrorClassFatal
		if classifier, ok := resolved.Adapter.(agents.ErrorClassifier); ok {
			class = classifier.ClassifyError(execResult.Events, execResult.ExitCode, execResult.Err)
		}
		attempt.Class = class
		attempts = append(attempts, attempt)
		last.Attempts = attempts
		r.emitLog(req.OnEvent, fmt.Sprintf("springfield: agent %s failed (exit %d): %v — classified %s", agentID, execResult.ExitCode, errText(execResult.Err), class))
		if class == agents.ErrorClassFatal {
			return last
		}
		r.emitLog(req.OnEvent, fmt.Sprintf("springfield: %s failure is retryable — falling through to the next agent in the chain", agentID))
		// Only install a cooldown for adapters that opted into Cooldowner.
		// Adapters without it (currently codex, gemini) fall through to the
		// next agent without being penalized for a single transient failure
		// — only the agent that knows how to recognize its own rate-limit
		// gets cooled down.
		if cd, ok := resolved.Adapter.(agents.Cooldowner); ok {
			until := cd.Cooldown(execResult.Events, execResult.ExitCode, execResult.Err, r.now())
			if until.IsZero() {
				until = r.now().Add(defaultCooldown)
			}
			r.installCooldown(agentID, until)
		}
	}

	if allSkipped {
		return Result{
			Status:    StatusFailed,
			Err:       fmt.Errorf("all agents in cooldown; retry after cooldowns expire"),
			StartedAt: start,
			EndedAt:   r.now(),
		}
	}
	return last
}

// emitLog writes a synthetic stderr event carrying a Springfield lifecycle
// line (dispatch, failure classification, fallthrough decision) so operators
// and the evidence trace can see why the chain advanced. No-op if handler is
// nil. Takes a plain string — not events — so it never counts as a transport
// parser under the enforcement staleness gate.
func (r *Runner) emitLog(handler exec.EventHandler, msg string) {
	if handler == nil {
		return
	}
	handler(exec.Event{Type: exec.EventStderr, Data: msg, Time: r.now()})
}

// errText renders err for a log line, tolerating nil (a clean-exit failure
// synthesized purely from exit code).
func errText(err error) string {
	if err == nil {
		return "no error detail"
	}
	return err.Error()
}

// emitSkipEvent writes a synthetic stderr event so operators see why an
// agent was skipped. No-op if handler is nil.
func (r *Runner) emitSkipEvent(handler exec.EventHandler, id agents.ID, until time.Time) {
	if handler == nil {
		return
	}
	handler(exec.Event{
		Type: exec.EventStderr,
		Data: fmt.Sprintf("springfield: %s in cooldown until %s; skipping", id, until.Format(time.RFC3339)),
		Time: r.now(),
	})
}

// AssistantText decodes the given agent's transcript into plain assistant
// text via that adapter's TranscriptDecoder, so the review gate can scan a
// verdict marker against real newlines instead of escaped stream-json. The
// registry is the runner's, so the agent boundary stays encapsulated here
// rather than threaded through the conductor. Falls back to the raw stdout
// concatenation when the agent is unknown or implements no decoder (e.g. a
// plain-text CLI), which keeps the anchored scan working for non-JSON output.
func (r *Runner) AssistantText(agent agents.ID, events []exec.Event) string {
	resolved, err := r.registry.Resolve(agents.ResolveInput{ProjectDefault: agent})
	if err == nil {
		if dec, ok := resolved.Adapter.(agents.TranscriptDecoder); ok {
			return dec.AssistantText(events)
		}
	}
	var stdout []string
	for _, e := range events {
		if e.Type == exec.EventStdout {
			stdout = append(stdout, e.Data)
		}
	}
	return strings.Join(stdout, "\n")
}

func normalizeAgentIDs(ids []agents.ID) []agents.ID {
	out := make([]agents.ID, 0, len(ids))
	seen := map[agents.ID]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
