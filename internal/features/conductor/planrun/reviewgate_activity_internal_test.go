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
)

// reviewActivityProbe is the review-gate analogue of activityProbeRunner: on
// every dispatch it reloads the persisted conductor state from disk (what a
// separate `springfield status` process would read) and records the plan's
// in-flight Activity as observed at that moment. A recorded phase=reviewing
// stamp therefore proves the gate stamped it via enterPhase AND SaveState'd it
// BEFORE running the reviewer — per-round persistence, not shared memory.
type reviewActivityProbe struct {
	root    string
	planID  string
	replies []coreruntime.Result
	seen    []*conductor.PlanActivity
	calls   int
	// panicOn is the 1-based dispatch index to panic on (0 = never), used to
	// drive the panic-exit path through the gate's deferred clear.
	panicOn int
}

func (r *reviewActivityProbe) Run(_ context.Context, _ coreruntime.Request) coreruntime.Result {
	r.calls++
	if proj, err := conductor.LoadProject(r.root); err == nil {
		if ps := proj.State.Plans[r.planID]; ps != nil {
			r.seen = append(r.seen, ps.Activity)
		} else {
			r.seen = append(r.seen, nil)
		}
	} else {
		r.seen = append(r.seen, nil)
	}
	if r.panicOn != 0 && r.calls == r.panicOn {
		panic("injected reviewer panic")
	}
	if r.calls > len(r.replies) {
		return coreruntime.Result{Agent: agents.AgentClaude}
	}
	return r.replies[r.calls-1]
}

// runningReviewGateInput builds a review-gate input wired for Activity progress:
// a freshly loaded project with a running PlanState, a deterministic clock, and
// the given probe as reviewer. The gate stamps/clears through this project.
func runningReviewGateInput(t *testing.T, probe *reviewActivityProbe, max int) (string, *conductor.Project, reviewGateInput) {
	t.Helper()
	root, project := loadedProject(t)
	project.State.Plans["feat"] = &conductor.PlanState{Status: conductor.StatusRunning}
	probe.root = root
	probe.planID = "feat"
	in := gateInput(probe, max)
	in.Project = project
	in.PlanID = "feat"
	in.Now = fixedNow(time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC))
	return root, project, in
}

// persistedActivity reloads state.json (a fresh status process) and returns the
// plan's persisted in-flight Activity, so the test observes durable truth rather
// than the gate's in-memory mutation.
func persistedActivity(t *testing.T, root string) *conductor.PlanActivity {
	t.Helper()
	proj, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	ps := proj.State.Plans["feat"]
	if ps == nil {
		return nil
	}
	return ps.Activity
}

// TestReviewGateStampsReviewingThenClearsOnEveryExit is the US-005 acceptance:
// the gate enters via enterPhase(reviewing) with a per-round counter (SaveState
// observable each round) and defers the clear so early-return, an injected
// error, and a panic each leave the round cleared and the phase OFF reviewing.
func TestReviewGateStampsReviewingThenClearsOnEveryExit(t *testing.T) {
	// pass-return: reviewer passes on round 1. The stamp is observed persisted
	// mid-gate; after the gate returns, the reviewing phase is cleared.
	t.Run("pass return clears", func(t *testing.T) {
		probe := &reviewActivityProbe{replies: []coreruntime.Result{
			{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<review-verdict>pass</review-verdict>")}},
		}}
		root, _, in := runningReviewGateInput(t, probe, 3)

		got := runReviewGate(in)
		if got.Outcome != reviewPassed {
			t.Fatalf("outcome = %v, want reviewPassed", got.Outcome)
		}
		if len(probe.seen) != 1 || probe.seen[0] == nil {
			t.Fatalf("expected reviewing stamp observed at the reviewer call, seen=%v", probe.seen)
		}
		if probe.seen[0].Phase != conductor.PhaseReviewing || probe.seen[0].Round != 1 {
			t.Fatalf("mid-gate stamp = %+v, want phase=reviewing round=1", probe.seen[0])
		}
		if act := persistedActivity(t, root); act != nil {
			t.Fatalf("gate return must clear the reviewing phase, got %+v", act)
		}
	})

	// per-round SaveState: revise(round 1) → fix → pass(round 2). The round
	// counter advances and each round's stamp is persisted (observable from
	// disk), proving SaveState-per-round rather than a single entry stamp.
	t.Run("per-round SaveState advances the counter", func(t *testing.T) {
		probe := &reviewActivityProbe{replies: []coreruntime.Result{
			{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<review-verdict>revise</review-verdict>")}}, // review 1
			{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<promise>COMPLETE</promise>")}},             // fix 1
			{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<review-verdict>pass</review-verdict>")}},   // review 2
		}}
		root, _, in := runningReviewGateInput(t, probe, 3)

		if got := runReviewGate(in); got.Outcome != reviewPassed {
			t.Fatalf("outcome = %v, want reviewPassed", got.Outcome)
		}
		if len(probe.seen) != 3 {
			t.Fatalf("expected review→fix→review = 3 dispatches, got %d", len(probe.seen))
		}
		if probe.seen[0] == nil || probe.seen[0].Round != 1 || probe.seen[0].Phase != conductor.PhaseReviewing {
			t.Fatalf("round 1 stamp = %+v, want phase=reviewing round=1", probe.seen[0])
		}
		if probe.seen[2] == nil || probe.seen[2].Round != 2 || probe.seen[2].Phase != conductor.PhaseReviewing {
			t.Fatalf("round 2 stamp = %+v, want phase=reviewing round=2 (per-round SaveState)", probe.seen[2])
		}
		if act := persistedActivity(t, root); act != nil {
			t.Fatalf("gate return must clear the reviewing phase, got %+v", act)
		}
	})

	// injected error: the reviewer agent fails → reviewErrored. The deferred
	// clear still fires, so the reviewing phase does not strand after the error.
	t.Run("injected error clears", func(t *testing.T) {
		boom := errors.New("reviewer boom")
		probe := &reviewActivityProbe{replies: []coreruntime.Result{
			{Agent: agents.AgentClaude, Err: boom},
		}}
		root, _, in := runningReviewGateInput(t, probe, 3)

		got := runReviewGate(in)
		if got.Outcome != reviewErrored || got.Err == nil {
			t.Fatalf("outcome = %v err=%v, want reviewErrored with err", got.Outcome, got.Err)
		}
		if probe.seen[0] == nil || probe.seen[0].Phase != conductor.PhaseReviewing {
			t.Fatalf("reviewing must be stamped before the error, seen=%v", probe.seen)
		}
		if act := persistedActivity(t, root); act != nil {
			t.Fatalf("errored return must clear the reviewing phase, got %+v", act)
		}
	})

	// panic: the reviewer panics mid-round. The gate does not recover, but its
	// deferred clear runs during unwinding, so the reviewing phase is cleared
	// even on the abnormal exit — a stranded phase is exactly the lie forbidden.
	t.Run("panic clears", func(t *testing.T) {
		probe := &reviewActivityProbe{panicOn: 1}
		root, _, in := runningReviewGateInput(t, probe, 3)

		func() {
			defer func() {
				if rec := recover(); rec == nil {
					t.Fatal("expected runReviewGate to propagate the reviewer panic")
				}
			}()
			runReviewGate(in)
		}()

		if probe.seen[0] == nil || probe.seen[0].Phase != conductor.PhaseReviewing || probe.seen[0].Round != 1 {
			t.Fatalf("reviewing must be stamped before the panic, seen=%v", probe.seen)
		}
		if act := persistedActivity(t, root); act != nil {
			t.Fatalf("panic unwinding must clear the reviewing phase via defer, got %+v", act)
		}
	})
}
