package planrun

import (
	"context"
	"errors"
	"testing"
	"time"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
	"springfield/internal/features/prd"
	"springfield/internal/features/verify"
)

// verifyActivityProbe is the verify-gate analogue of reviewActivityProbe: it
// stands in for the verify COMMAND (the first thing each round runs, right after
// the stamp) and on every call reloads the persisted conductor state from disk
// (what a separate `springfield status` process would read), recording the
// plan's in-flight Activity as observed at that moment. A recorded
// phase=verifying stamp therefore proves the gate stamped it via enterPhase AND
// SaveState'd it BEFORE running the command — per-round persistence, not shared
// memory.
type verifyActivityProbe struct {
	root    string
	planID  string
	results []verify.Result
	seen    []*conductor.PlanActivity
	calls   int
	// panicOn is the 1-based command index to panic on (0 = never), used to drive
	// the panic-exit path through the gate's deferred clear.
	panicOn int
}

func (p *verifyActivityProbe) run(_ context.Context, _ verify.Request) verify.Result {
	p.calls++
	if proj, err := conductor.LoadProject(p.root); err == nil {
		if ps := proj.State.Plans[p.planID]; ps != nil {
			p.seen = append(p.seen, ps.Activity)
		} else {
			p.seen = append(p.seen, nil)
		}
	} else {
		p.seen = append(p.seen, nil)
	}
	if p.panicOn != 0 && p.calls == p.panicOn {
		panic("injected verify-command panic")
	}
	if p.calls > len(p.results) {
		return verify.Result{ExitCode: 0}
	}
	return p.results[p.calls-1]
}

// runningVerifyGateInput builds a verify-gate input wired for Activity progress:
// a freshly loaded project with a running PlanState, a deterministic clock, and
// the given probe as the command. The gate stamps/clears through this project.
func runningVerifyGateInput(t *testing.T, probe *verifyActivityProbe, fix AgentRunner, max int) (string, *conductor.Project, verifyGateInput) {
	t.Helper()
	root, project := loadedProject(t)
	project.State.Plans["feat"] = &conductor.PlanState{Status: conductor.StatusRunning}
	probe.root = root
	probe.planID = "feat"
	in := verifyGateInput{
		Project:           project,
		PlanID:            "feat",
		Now:               fixedNow(time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)),
		Command:           probe.run,
		Runner:            fix,
		ImplementerAgents: []agents.ID{agents.AgentClaude},
		VerifyCommand:     "go test ./...",
		Timeout:           20 * time.Minute,
		MaxIterations:     max,
		WorktreeRoot:      "/tmp/wt",
		PRD: prd.PRD{
			ID:          "feat",
			UserStories: []prd.UserStory{{ID: "US-1", AcceptanceCriteria: []string{"works"}}},
		},
		EvidenceDir: t.TempDir(),
	}
	return root, project, in
}

// TestVerifyGateStampsVerifyingThenClearsOnEveryExit is the US-006 acceptance:
// the gate enters via enterPhase(verifying) with a per-round counter (SaveState
// observable each round) and defers the clear so early-return, an injected
// launch error, and a panic each leave the round cleared and the phase OFF
// verifying. This is the positive proof that pairs with the enforcement scan's
// negative guarantee — together they show the verify gate is covered.
func TestVerifyGateStampsVerifyingThenClearsOnEveryExit(t *testing.T) {
	// pass-return: command exits 0 on round 1. The stamp is observed persisted
	// mid-gate; after the gate returns, the verifying phase is cleared.
	t.Run("pass return clears", func(t *testing.T) {
		probe := &verifyActivityProbe{results: []verify.Result{{ExitCode: 0}}}
		root, _, in := runningVerifyGateInput(t, probe, &seqRunner{}, 3)

		got := runVerifyGate(in)
		if got.Outcome != verifyPassed {
			t.Fatalf("outcome = %v, want verifyPassed", got.Outcome)
		}
		if len(probe.seen) != 1 || probe.seen[0] == nil {
			t.Fatalf("expected verifying stamp observed at the command call, seen=%v", probe.seen)
		}
		if probe.seen[0].Phase != conductor.PhaseVerifying || probe.seen[0].Round != 1 {
			t.Fatalf("mid-gate stamp = %+v, want phase=verifying round=1", probe.seen[0])
		}
		if act := persistedActivity(t, root); act != nil {
			t.Fatalf("gate return must clear the verifying phase, got %+v", act)
		}
	})

	// per-round SaveState: fail(round 1) → fix → pass(round 2). The round counter
	// advances and each round's stamp is persisted (observable from disk), proving
	// SaveState-per-round rather than a single entry stamp.
	t.Run("per-round SaveState advances the counter", func(t *testing.T) {
		probe := &verifyActivityProbe{results: []verify.Result{
			{ExitCode: 1, Stderr: "FAIL"}, // round 1
			{ExitCode: 0},                 // round 2
		}}
		fix := &seqRunner{results: []coreruntime.Result{
			{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<promise>COMPLETE</promise>")}},
		}}
		root, _, in := runningVerifyGateInput(t, probe, fix, 3)

		if got := runVerifyGate(in); got.Outcome != verifyPassed {
			t.Fatalf("outcome = %v, want verifyPassed", got.Outcome)
		}
		if len(probe.seen) != 2 {
			t.Fatalf("expected command→fix→command = 2 command calls, got %d", len(probe.seen))
		}
		if probe.seen[0] == nil || probe.seen[0].Round != 1 || probe.seen[0].Phase != conductor.PhaseVerifying {
			t.Fatalf("round 1 stamp = %+v, want phase=verifying round=1", probe.seen[0])
		}
		if probe.seen[1] == nil || probe.seen[1].Round != 2 || probe.seen[1].Phase != conductor.PhaseVerifying {
			t.Fatalf("round 2 stamp = %+v, want phase=verifying round=2 (per-round SaveState)", probe.seen[1])
		}
		if act := persistedActivity(t, root); act != nil {
			t.Fatalf("gate return must clear the verifying phase, got %+v", act)
		}
	})

	// injected error: the command could not launch (res.Err set) → verifyErrored.
	// The deferred clear still fires, so the verifying phase does not strand after
	// the error.
	t.Run("injected error clears", func(t *testing.T) {
		boom := errors.New("chdir: no such directory")
		probe := &verifyActivityProbe{results: []verify.Result{{ExitCode: -1, Err: boom}}}
		root, _, in := runningVerifyGateInput(t, probe, &seqRunner{}, 3)

		got := runVerifyGate(in)
		if got.Outcome != verifyErrored || got.Err == nil {
			t.Fatalf("outcome = %v err=%v, want verifyErrored with err", got.Outcome, got.Err)
		}
		if probe.seen[0] == nil || probe.seen[0].Phase != conductor.PhaseVerifying {
			t.Fatalf("verifying must be stamped before the error, seen=%v", probe.seen)
		}
		if act := persistedActivity(t, root); act != nil {
			t.Fatalf("errored return must clear the verifying phase, got %+v", act)
		}
	})

	// panic: the command panics mid-round. The gate does not recover, but its
	// deferred clear runs during unwinding, so the verifying phase is cleared even
	// on the abnormal exit — a stranded phase is exactly the lie forbidden.
	t.Run("panic clears", func(t *testing.T) {
		probe := &verifyActivityProbe{panicOn: 1}
		root, _, in := runningVerifyGateInput(t, probe, &seqRunner{}, 3)

		func() {
			defer func() {
				if rec := recover(); rec == nil {
					t.Fatal("expected runVerifyGate to propagate the command panic")
				}
			}()
			runVerifyGate(in)
		}()

		if probe.seen[0] == nil || probe.seen[0].Phase != conductor.PhaseVerifying || probe.seen[0].Round != 1 {
			t.Fatalf("verifying must be stamped before the panic, seen=%v", probe.seen)
		}
		if act := persistedActivity(t, root); act != nil {
			t.Fatalf("panic unwinding must clear the verifying phase via defer, got %+v", act)
		}
	})
}
