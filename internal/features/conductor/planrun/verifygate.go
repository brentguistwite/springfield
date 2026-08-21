package planrun

import (
	"context"
	"fmt"
	"time"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
	"springfield/internal/features/prd"
	"springfield/internal/features/verify"
)

// verifyCommandFunc is the command boundary the gate depends on. Production
// passes verify.Run directly (its signature IS this type); tests pass a scripted
// stub. A func type rather than an interface keeps the seam a single obvious
// contract with no adapter.
type verifyCommandFunc func(ctx context.Context, req verify.Request) verify.Result

// stallController is the slice's event-recency stall detector as the verify gate
// consumes it: the gate heartbeats it (Observe) on every fix-iteration agent event
// and suppresses it (Suppress) for the duration of each verify command run. It is
// the narrow read of *stall.Detector — the gate never watches or builds it (the
// caller owns the watcher lifecycle). Nil disables gate-level stall handling.
type stallController interface {
	Observe()
	Suppress(bool)
}

// verifyOutcome is the gate's three-way verdict, mirroring reviewOutcome.
// verifyPassed means the command exited 0; verifyNeedsHuman means the fix-loop
// exhausted its budget OR two consecutive rounds timed out; verifyErrored means
// the command could not be launched or a fix-iteration agent invocation failed.
type verifyOutcome int

const (
	verifyPassed verifyOutcome = iota
	verifyNeedsHuman
	verifyErrored
)

// verifyGateInput is everything the verify fix-loop needs. Command, Runner, and
// TamperGuard are injected so the gate is unit-testable with fakes. Command,
// Timeout, and MaxIterations are the already-resolved effective values (see
// config.ResolveVerify) — the gate does not re-derive defaults.
type verifyGateInput struct {
	Ctx context.Context
	// Project + PlanID + Now are the in-flight Activity plumbing, mirroring the
	// review gate: each round the gate stamps phase=verifying with its round
	// counter through enterPhase and defers a clear so leaving the gate
	// (return/error/panic) never strands the verifying phase. All three may be
	// zero — a nil Project makes enterPhase a silent no-op, so a caller that does
	// not wire progress (older tests) still runs the gate unchanged.
	Project           *conductor.Project
	PlanID            string
	Now               func() time.Time
	Command           verifyCommandFunc
	Runner            AgentRunner
	ImplementerAgents []agents.ID
	ExecutionSettings agents.ExecutionSettings
	VerifyCommand     string
	Timeout           time.Duration
	MaxIterations     int
	// PortEnv is the slice's port block (SPRINGFIELD_PORT/SPRINGFIELD_PORT_RANGE)
	// injected into BOTH the verify command's environment AND each fix-iteration
	// agent dispatch, so the verify suite and any server a fix agent starts bind
	// the same ports the main story loop and setup command saw. Nil leaves the
	// environment untouched.
	PortEnv         map[string]string
	WorktreeRoot    string
	PRD             prd.PRD
	ContextMD       string
	ProjectGuidance string
	ProjectRoot     string
	EvidenceDir     string
	OnEvent         coreexec.EventHandler
	// TamperGuard, when non-nil, wraps each fix-iteration agent run: the
	// implementer can touch .springfield/ and the same protection the main story
	// loop uses must apply here. The verify command itself is a deterministic
	// process, not a prompt-injectable agent, so it is NOT guarded.
	TamperGuard TamperGuard
	// Stall, when non-nil, is the slice's shared event-recency stall detector. The
	// gate SUPPRESSES it for the duration of each verify command run — a churning
	// test suite is a legitimately busy but event-quiet phase that streams nothing
	// through the agent event path and would otherwise accrue idle time toward a
	// false wedge — and heartbeats it on every fix-iteration agent event so a
	// genuinely wedged fix agent is still classified. Nil leaves the gate
	// unmonitored (stall detection disabled, or a legacy caller that does not wire
	// it). See [SinglePlanInput.newVerifyStall] for the production construction.
	Stall stallController
}

type verifyGateResult struct {
	Outcome verifyOutcome
	// Reason is a short human-readable explanation for a needs-human outcome
	// (exhaustion vs. consecutive timeouts), surfaced in status/recover.
	Reason string
	Err    error
}

// runVerifyGate runs the verify fix-loop with its OWN round counter (independent
// of both single_workstream_iterations and the review gate's counter). One round
// = one command run + (on failure, if budget remains) one implementer fix
// iteration. exit 0 → verifyPassed; exhausting MaxIterations → verifyNeedsHuman;
// two CONSECUTIVE timeouts → early verifyNeedsHuman (a stuck suite will not
// un-stick on retry); a launch failure or a fix-agent error → verifyErrored.
// Purpose-built evidence (verify.json + stdout/stderr) is written every round.
func runVerifyGate(in verifyGateInput) verifyGateResult {
	max := in.MaxIterations
	if max < 1 {
		max = 1
	}

	ctx := in.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	// Matched entry-stamp / exit-clear, identical to the review gate: every round
	// stamps phase=verifying (with its round counter) through enterPhase, and this
	// deferred clear drops the fine signal on EVERY exit — pass/needs-human/errored
	// return, or a panic unwinding through the loop — so the verifying phase is
	// never stranded after the gate hands control back. A reader then falls back to
	// the derived coarse phase.
	defer clearActivity(in.Project, in.PlanID, in.Now)

	req := verify.Request{
		Command: in.VerifyCommand,
		Dir:     in.WorktreeRoot,
		Env:     in.PortEnv,
		Timeout: in.Timeout,
	}

	// fixOnEvent heartbeats the shared stall detector on every fix-iteration agent
	// event before forwarding to the caller's handler, so a churning fix agent
	// keeps its idle timer fresh and is not misclassified as wedged. Nil detector
	// (detection disabled) leaves the caller's handler untouched.
	fixOnEvent := in.OnEvent
	if in.Stall != nil {
		inner := in.OnEvent
		fixOnEvent = func(e coreexec.Event) {
			in.Stall.Observe()
			if inner != nil {
				inner(e)
			}
		}
	}

	consecutiveTimeouts := 0

	for round := 1; ; round++ {
		if err := ctx.Err(); err != nil {
			return verifyGateResult{Outcome: verifyErrored, Err: err}
		}

		// Stamp the in-flight Activity BEFORE the command runs so a concurrent
		// status read observes phase=verifying with this round. A save failure
		// degrades to the derived coarse phase (Tier 1), never a lie, so it is a
		// best-effort progress stamp and does not fail the gate.
		_ = enterPhase(in.Project, in.PlanID, conductor.PhaseVerifying, "", round, in.Now)

		// The verify command is a legitimately busy but event-quiet phase (a
		// churning test suite streams nothing through the agent event path), so
		// suppress wedge classification for its whole duration. Suppress(false)
		// resets the idle timer on resume so a long suite does not instantly flag
		// when the fix loop picks back up.
		if in.Stall != nil {
			in.Stall.Suppress(true)
		}
		res := in.Command(ctx, req)
		if in.Stall != nil {
			in.Stall.Suppress(false)
		}
		if _, wErr := verify.WriteEvidence(in.EvidenceDir, round, req, res); wErr != nil && in.OnEvent != nil {
			in.OnEvent(coreexec.Event{Type: coreexec.EventStderr, Data: fmt.Sprintf(
				"WARN: could not write verify evidence for round %d: %v", round, wErr,
			)})
		}

		// A launch failure (Err set) is not a fixable round — there is nothing
		// the implementer can fix if the command never started (e.g. a missing
		// working directory). Surface it as errored.
		if res.Err != nil {
			return verifyGateResult{Outcome: verifyErrored, Err: fmt.Errorf("verify command could not launch (round %d): %w", round, res.Err)}
		}

		if res.ExitCode == 0 && !res.TimedOut {
			return verifyGateResult{Outcome: verifyPassed}
		}

		// Caller/context abort (e.g. SIGINT) killed the command mid-run: the
		// non-zero exit is the KILL, not a fixable failure. Dispatching a fix here
		// would run an implementer against an already-cancelled context and could
		// persist a false verify-needs-human diagnosis for what was a user abort.
		// Surface it as errored BEFORE the fix-loop, propagating ctx.Err.
		if res.Cancelled || ctx.Err() != nil {
			err := ctx.Err()
			if err == nil {
				err = context.Canceled
			}
			return verifyGateResult{Outcome: verifyErrored, Err: err}
		}

		// Failed round. Track consecutive timeouts so a suite that hangs twice in
		// a row escalates early instead of burning the whole budget.
		if res.TimedOut {
			consecutiveTimeouts++
			if consecutiveTimeouts >= 2 {
				return verifyGateResult{
					Outcome: verifyNeedsHuman,
					Reason:  fmt.Sprintf("verify command timed out %d rounds in a row (%q); a stuck suite will not un-stick on retry", consecutiveTimeouts, in.VerifyCommand),
				}
			}
		} else {
			consecutiveTimeouts = 0
		}

		if round >= max {
			reason := fmt.Sprintf("verify command still failing after %d iterations (%q)", max, in.VerifyCommand)
			if res.TimedOut {
				// The final budgeted round timed out (without the two-in-a-row early
				// escalation firing). Name the timeout so the exhaustion reason is not
				// silently generic — the operator needs to know the suite hung.
				reason += " (last round timed out)"
			}
			return verifyGateResult{
				Outcome: verifyNeedsHuman,
				Reason:  reason,
			}
		}

		fixPrompt, err := BuildVerifyFixPrompt(in.PRD, in.ContextMD, in.ProjectGuidance, in.ProjectRoot, req, res)
		if err != nil {
			return verifyGateResult{Outcome: verifyErrored, Err: fmt.Errorf("build verify-fix prompt (round %d): %w", round, err)}
		}

		// Snapshot the control plane before the fix-iteration agent runs — the
		// implementer can touch .springfield/ and the same protection the main
		// story loop uses must apply here.
		if in.TamperGuard != nil {
			if snapErr := in.TamperGuard.Snapshot(); snapErr != nil {
				return verifyGateResult{Outcome: verifyErrored, Err: fmt.Errorf("tamper guard snapshot (verify-fix %d): %w", round, snapErr)}
			}
		}
		fix := in.Runner.Run(ctx, coreruntime.Request{
			AgentIDs:          in.ImplementerAgents,
			Prompt:            fixPrompt,
			WorkDir:           in.WorktreeRoot,
			Env:               in.PortEnv,
			OnEvent:           fixOnEvent,
			ExecutionSettings: in.ExecutionSettings,
		})
		writeReviewEvidence(in.EvidenceDir, fmt.Sprintf("verify-fix-%d", round), fixPrompt, string(fix.Agent), fix.Events, fix.Err)
		if in.TamperGuard != nil {
			tamperReason, detectErr := in.TamperGuard.Detect()
			if detectErr != nil {
				return verifyGateResult{Outcome: verifyErrored, Err: fmt.Errorf("tamper guard detect (verify-fix %d): %w", round, detectErr)}
			}
			if tamperReason != "" {
				if restoreErr := in.TamperGuard.Restore(); restoreErr != nil {
					return verifyGateResult{Outcome: verifyErrored, Err: fmt.Errorf("tamper-detected during verify-fix %d: %s (restore also failed: %w)", round, tamperReason, restoreErr)}
				}
				return verifyGateResult{Outcome: verifyErrored, Err: fmt.Errorf("tamper-detected during verify-fix %d: %s", round, tamperReason)}
			}
		}
		if fix.Err != nil {
			return verifyGateResult{Outcome: verifyErrored, Err: fmt.Errorf("verify-fix iteration failed (round %d): %w", round, fix.Err)}
		}
	}
}

// compile-time assertion: verify.Run must satisfy the command seam so a
// signature drift in the verify package fails the build here, not at the US-006
// wiring site.
var _ verifyCommandFunc = verify.Run
