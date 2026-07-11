package planrun_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/config"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planrun"
	"springfield/internal/features/prd"
	"springfield/internal/features/verify"
)

// stubVerify returns a scripted verify command boundary that never spawns a real
// process: every round yields the same exit code / timed-out verdict. Used to
// drive finishWithGates' verify-before-review ordering deterministically.
func stubVerify(exitCode int, timedOut bool) func(context.Context, verify.Request) verify.Result {
	return func(_ context.Context, _ verify.Request) verify.Result {
		return verify.Result{ExitCode: exitCode, TimedOut: timedOut}
	}
}

// verifyEnabled is the project-global [verify] block used by these tests. A
// MaxVerifyIterations of 1 makes a failing verify escalate to needs-human on the
// very first round with ZERO fix-agent dispatches, so any residual runner call
// can only be the review gate — making "review NOT called" a clean count check.
func verifyEnabled() config.VerifyConfig {
	return config.VerifyConfig{Enabled: true, Command: "go test ./...", MaxVerifyIterations: 1}
}

// TestVerifyGateFailsBeforeReviewAtNormalCompletion pins the primary ordering
// invariant at the normal completion site (completeHonored): a story finishes
// clean (pass + COMPLETE), verify then runs and FAILS, so the plan terminates
// needs-human and the review gate is asserted NOT to run.
func TestVerifyGateFailsBeforeReviewAtNormalCompletion(t *testing.T) {
	root := projectFixtureWithUnpassedStory(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()

	// Only ONE agent call is legitimate: the story dispatch. If review ran it
	// would be call 2 — the count assertion below catches that regression.
	runner := &queuedAgentRunner{results: []coreruntime.Result{
		{
			Agent:  agents.AgentClaude,
			Status: coreruntime.StatusPassed,
			Events: []coreexec.Event{{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>"}},
		},
	}}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:       project,
		ControlRoot:   root,
		WorktreeBase:  ".worktrees",
		AgentIDs:      []agents.ID{agents.AgentClaude},
		Runner:        runner,
		Manager:       &planrun.Manager{Git: g},
		ReviewConfig:  config.ReviewConfig{Enabled: true},
		VerifyConfig:  verifyEnabled(),
		VerifyCommand: stubVerify(1, false), // verify FAILS
	})

	if res.Status != conductor.StatusNeedsHuman {
		t.Fatalf("Status = %v, want StatusNeedsHuman (verify failure must halt for human)", res.Status)
	}
	if res.Err == nil {
		t.Fatal("Err must be non-nil so the batch loop halts on verify failure")
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected exactly 1 agent call (story only; review must NOT run after verify fails), got %d", len(runner.calls))
	}

	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	st := reloaded.State.Plans["alpha"]
	if st == nil {
		t.Fatal("no persisted state for alpha")
	}
	if st.Status != conductor.StatusNeedsHuman {
		t.Fatalf("persisted Status = %v, want StatusNeedsHuman", st.Status)
	}
	if st.Merge != nil {
		t.Fatalf("Merge must be nil on needs-human (Integrate must not run); got %+v", st.Merge)
	}
}

// TestVerifyGatePassThenReviewAtNormalCompletion pins the pass path: verify
// exits 0, so the review gate DOES run afterward. Both gates passing yields a
// completed plan reached via TWO agent calls (story + review).
func TestVerifyGatePassThenReviewAtNormalCompletion(t *testing.T) {
	root := projectFixtureWithUnpassedStory(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()

	// Call 1: story → pass + COMPLETE. Call 2: review → pass verdict.
	runner := &queuedAgentRunner{results: []coreruntime.Result{
		{
			Agent:  agents.AgentClaude,
			Status: coreruntime.StatusPassed,
			Events: []coreexec.Event{{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>"}},
		},
		{
			Agent:  agents.AgentClaude,
			Status: coreruntime.StatusPassed,
			Events: []coreexec.Event{{Type: coreexec.EventStdout, Data: "<review-verdict>pass</review-verdict>"}},
		},
	}}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:       project,
		ControlRoot:   root,
		WorktreeBase:  ".worktrees",
		AgentIDs:      []agents.ID{agents.AgentClaude},
		Runner:        runner,
		Manager:       &planrun.Manager{Git: g},
		ReviewConfig:  config.ReviewConfig{Enabled: true},
		VerifyConfig:  verifyEnabled(),
		VerifyCommand: stubVerify(0, false), // verify PASSES
	})

	if res.Err != nil {
		t.Fatalf("verify pass + review pass must complete cleanly, got: %v", res.Err)
	}
	if res.Status != conductor.StatusCompleted {
		t.Fatalf("Status = %v, want StatusCompleted", res.Status)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 agent calls (story + review after verify pass), got %d", len(runner.calls))
	}
}

// TestVerifyGateFailsBeforeReviewAtCrashComplete pins verify ordering at the
// crash-complete site: the agent emits every marker then exits non-zero
// (post-completion crash judged complete), verify runs and FAILS, so the plan
// is needs-human and review does not run.
func TestVerifyGateFailsBeforeReviewAtCrashComplete(t *testing.T) {
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
		},
	}
	root, project := projectFixtureWithPRD(t, "feat", p)
	g := newFakeGit() // Head() ≠ base head, so the worktree-advanced check holds.

	// Story dispatch passes + COMPLETE then crashes non-zero. The crash is judged
	// post-completion teardown noise, so finishWithGates runs.
	runner := &queuedAgentRunner{results: []coreruntime.Result{
		{
			Agent:    agents.AgentClaude,
			Status:   coreruntime.StatusFailed,
			ExitCode: 1,
			Err:      errors.New("claude API 400 after completion"),
			Events: []coreexec.Event{
				{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>", Time: time.Now()},
			},
			StartedAt: time.Now().Add(-time.Second),
			EndedAt:   time.Now(),
		},
	}}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:       project,
		ControlRoot:   root,
		WorktreeBase:  ".worktrees",
		AgentIDs:      []agents.ID{agents.AgentClaude},
		Runner:        runner,
		Manager:       &planrun.Manager{Git: g},
		ProjectRoot:   root,
		ReviewConfig:  config.ReviewConfig{Enabled: true},
		VerifyConfig:  verifyEnabled(),
		VerifyCommand: stubVerify(1, false), // verify FAILS
	})

	if res.Status != conductor.StatusNeedsHuman {
		t.Fatalf("Status = %v, want StatusNeedsHuman (verify failure at crash-complete site)", res.Status)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected exactly 1 agent call (story only; review must NOT run), got %d", len(runner.calls))
	}

	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	st := reloaded.State.Plans["feat"]
	if st == nil {
		t.Fatal("no persisted state for feat")
	}
	if st.Status != conductor.StatusNeedsHuman {
		t.Fatalf("persisted Status = %v, want StatusNeedsHuman", st.Status)
	}
	if st.Merge != nil {
		t.Fatalf("Merge must be nil on needs-human; got %+v", st.Merge)
	}
}

// TestVerifyGateFailsBeforeReviewAtTopOfLoopResume pins verify ordering at the
// top-of-loop resume site: the plan re-enters with ALL stories already passed
// (e.g. a needs-human retry), so NextStory returns PickAllPassed on the first
// call and finishWithGates fires with ZERO story dispatch. Verify FAILS →
// needs-human, and because verify short-circuits review, the agent runner is
// never called at all.
func TestVerifyGateFailsBeforeReviewAtTopOfLoopResume(t *testing.T) {
	root := projectFixture(t, "alpha") // single story already passed
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()

	// No scripted results: any call is a bug. Review enabled proves the short-
	// circuit — if verify did NOT gate first, review would dispatch (call 1).
	runner := &queuedAgentRunner{}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:       project,
		ControlRoot:   root,
		WorktreeBase:  ".worktrees",
		AgentIDs:      []agents.ID{agents.AgentClaude},
		Runner:        runner,
		Manager:       &planrun.Manager{Git: g},
		ReviewConfig:  config.ReviewConfig{Enabled: true},
		VerifyConfig:  verifyEnabled(),
		VerifyCommand: stubVerify(1, false), // verify FAILS
	})

	if res.Status != conductor.StatusNeedsHuman {
		t.Fatalf("Status = %v, want StatusNeedsHuman (verify at top-of-loop must fire and fail)", res.Status)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("expected ZERO agent calls (no story, review short-circuited by verify), got %d", len(runner.calls))
	}

	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	st := reloaded.State.Plans["alpha"]
	if st == nil {
		t.Fatal("no persisted state for alpha")
	}
	if st.Status != conductor.StatusNeedsHuman {
		t.Fatalf("persisted Status = %v, want StatusNeedsHuman", st.Status)
	}
	if st.Merge != nil {
		t.Fatalf("Merge must be nil on needs-human; got %+v", st.Merge)
	}
}

// TestVerifyGateErroredYieldsStatusFailed pins the third terminal state: when
// the verify command cannot launch (Result.Err set), the gate returns errored
// and the plan surfaces as StatusFailed — not needs-human — with review skipped.
func TestVerifyGateErroredYieldsStatusFailed(t *testing.T) {
	root := projectFixture(t, "alpha") // story already passed
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()
	runner := &queuedAgentRunner{}

	launchErr := func(_ context.Context, _ verify.Request) verify.Result {
		return verify.Result{ExitCode: -1, Err: errors.New("chdir: no such directory")}
	}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:       project,
		ControlRoot:   root,
		WorktreeBase:  ".worktrees",
		AgentIDs:      []agents.ID{agents.AgentClaude},
		Runner:        runner,
		Manager:       &planrun.Manager{Git: g},
		ReviewConfig:  config.ReviewConfig{Enabled: true},
		VerifyConfig:  verifyEnabled(),
		VerifyCommand: launchErr,
	})

	if res.Status != conductor.StatusFailed {
		t.Fatalf("Status = %v, want StatusFailed (verify launch failure is not human-recoverable)", res.Status)
	}
	if res.Err == nil {
		t.Fatal("Err must be non-nil when verify errors")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("expected ZERO agent calls (review must not run after verify error), got %d", len(runner.calls))
	}
}

// TestVerifyGateCancelledYieldsVerifyErrored closes the gap the round-1 commit
// claims ("returns verify-errored on cancel"): when the verify command is killed
// by a caller/context abort (Result.Cancelled) rather than exiting non-zero, the
// gate must surface verifyErrored — NOT verify-needs-human — so a user abort is
// never persisted as a fixable failed round. The terminal plan is StatusFailed
// with ExitReason "verify-errored", and review is skipped.
func TestVerifyGateCancelledYieldsVerifyErrored(t *testing.T) {
	root := projectFixture(t, "alpha") // story already passed → top-of-loop verify
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()
	runner := &queuedAgentRunner{}

	// Verify command is killed by a caller abort: Cancelled=true, ExitCode=-1
	// (the kill), TimedOut=false. This is NOT an ordinary failed round.
	cancelled := func(_ context.Context, _ verify.Request) verify.Result {
		return verify.Result{ExitCode: -1, Cancelled: true}
	}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:       project,
		ControlRoot:   root,
		WorktreeBase:  ".worktrees",
		AgentIDs:      []agents.ID{agents.AgentClaude},
		Runner:        runner,
		Manager:       &planrun.Manager{Git: g},
		ReviewConfig:  config.ReviewConfig{Enabled: true},
		VerifyConfig:  verifyEnabled(),
		VerifyCommand: cancelled,
	})

	if res.Status != conductor.StatusFailed {
		t.Fatalf("Status = %v, want StatusFailed (a cancel is verify-errored, not human-recoverable)", res.Status)
	}
	if res.Err == nil {
		t.Fatal("Err must be non-nil when verify is cancelled")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("expected ZERO agent calls (no fix, review skipped on cancel), got %d", len(runner.calls))
	}

	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	st := reloaded.State.Plans["alpha"]
	if st == nil {
		t.Fatal("no persisted state for alpha")
	}
	if st.Status != conductor.StatusFailed {
		t.Fatalf("persisted Status = %v, want StatusFailed", st.Status)
	}
	if st.ExitReason != "verify-errored" {
		t.Fatalf("ExitReason = %q, want verify-errored (NOT verify-needs-human) on cancel", st.ExitReason)
	}
}

// TestVerifyDisabledLeavesMarkerOnlyCompletion pins the US-008 opt-in regression
// guard: an unconfigured plan (no [verify] config, no per-plan verify override,
// AND no review) completes purely on markers, exactly as before the verify gate
// existed. The verify command boundary is a fail-the-test tripwire — proving the
// gate is skipped ENTIRELY, not merely that its verdict happened to pass.
func TestVerifyDisabledLeavesMarkerOnlyCompletion(t *testing.T) {
	root := projectFixtureWithUnpassedStory(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()

	// Single dispatch: story passes + COMPLETE. No review, no verify → the plan
	// must complete on the marker alone.
	runner := &queuedAgentRunner{results: []coreruntime.Result{
		{
			Agent:  agents.AgentClaude,
			Status: coreruntime.StatusPassed,
			Events: []coreexec.Event{{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>"}},
		},
	}}

	// Tripwire: if the disabled gate ever dispatches the verify command, fail loudly.
	verifyInvoked := false
	tripwire := func(_ context.Context, _ verify.Request) verify.Result {
		verifyInvoked = true
		t.Error("verify command must NOT run when verify is unconfigured")
		return verify.Result{ExitCode: 0}
	}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:       project,
		ControlRoot:   root,
		WorktreeBase:  ".worktrees",
		AgentIDs:      []agents.ID{agents.AgentClaude},
		Runner:        runner,
		Manager:       &planrun.Manager{Git: g},
		VerifyCommand: tripwire,
		// VerifyConfig zero value: gate disabled. ReviewConfig omitted: marker-only.
	})

	if res.Err != nil {
		t.Fatalf("marker-only completion must succeed with verify disabled, got: %v", res.Err)
	}
	if res.Status != conductor.StatusCompleted {
		t.Fatalf("Status = %v, want StatusCompleted (markers alone complete the plan)", res.Status)
	}
	if verifyInvoked {
		t.Fatal("verify command was invoked despite no [verify] config")
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected exactly 1 agent call (story only; no verify, no review), got %d", len(runner.calls))
	}

	// The completed plan must be queued for merge integration exactly as a
	// pre-verify-gate marker completion was — the gate did not alter the terminal shape.
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	st := reloaded.State.Plans["alpha"]
	if st == nil {
		t.Fatal("no persisted state for alpha")
	}
	if st.Status != conductor.StatusCompleted {
		t.Fatalf("persisted Status = %v, want StatusCompleted", st.Status)
	}
	if st.Merge == nil || st.Merge.Status != conductor.MergePending {
		t.Fatalf("completed marker-only plan must be pending merge integration; got Merge=%+v", st.Merge)
	}

	// No verify evidence may exist: skipping the gate means writing nothing under
	// verify-iter-*. Any such dir proves the gate ran a round.
	if entries, _ := filepath.Glob(filepath.Join(res.EvidencePath, "verify-iter-*")); len(entries) != 0 {
		t.Fatalf("verify evidence written despite disabled gate: %v", entries)
	}
}

// TestVerifyDisabledLeavesReviewOnlyBehavior pins the opt-in guarantee: with no
// [verify] config, finishWithGates behaves exactly as the old finishWithReview —
// review runs directly with no verify round.
func TestVerifyDisabledLeavesReviewOnlyBehavior(t *testing.T) {
	root := projectFixture(t, "alpha") // story already passed
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()

	// Only the review runs (top-of-loop, story pre-passed): 1 call, pass verdict.
	runner := &queuedAgentRunner{results: []coreruntime.Result{
		{
			Agent:  agents.AgentClaude,
			Status: coreruntime.StatusPassed,
			Events: []coreexec.Event{{Type: coreexec.EventStdout, Data: "<review-verdict>pass</review-verdict>"}},
		},
	}}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
		ReviewConfig: config.ReviewConfig{Enabled: true},
		// VerifyConfig zero value: gate disabled.
	})

	if res.Err != nil {
		t.Fatalf("verify-disabled + review pass must complete, got: %v", res.Err)
	}
	if res.Status != conductor.StatusCompleted {
		t.Fatalf("Status = %v, want StatusCompleted", res.Status)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected exactly 1 agent call (review only, no verify), got %d", len(runner.calls))
	}
}
