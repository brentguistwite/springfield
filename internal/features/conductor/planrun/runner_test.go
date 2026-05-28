package planrun_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/config"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planrun"
)

// fakeAgentRunner is an in-memory AgentRunner for SinglePlan tests.
type fakeAgentRunner struct {
	calls       []coreruntime.Request
	failure     bool
	beforeReply func()
	// events are injected into the success result for marker scanning.
	events []coreexec.Event
}

func (f *fakeAgentRunner) Run(_ context.Context, req coreruntime.Request) coreruntime.Result {
	f.calls = append(f.calls, req)
	if f.beforeReply != nil {
		f.beforeReply()
	}
	if f.failure {
		return coreruntime.Result{
			Agent:    agents.AgentClaude,
			Status:   coreruntime.StatusFailed,
			ExitCode: 1,
			Err:      nil,
		}
	}
	return coreruntime.Result{
		Agent:     agents.AgentClaude,
		Status:    coreruntime.StatusPassed,
		ExitCode:  0,
		Events:    f.events,
		StartedAt: time.Now().Add(-time.Second),
		EndedAt:   time.Now(),
	}
}

// sabotageStateJSON replaces .springfield/execution/state.json with a
// directory so the next SaveState write fails. Used to drive the
// terminal-save failure branches.
func sabotageStateJSON(t *testing.T, root string) {
	t.Helper()
	statePath := filepath.Join(root, ".springfield", "execution", "state.json")
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove state.json: %v", err)
	}
	if err := os.MkdirAll(statePath, 0o755); err != nil {
		t.Fatalf("mkdir state.json: %v", err)
	}
}

// projectFixture writes a minimal Springfield project with one plan unit so
// LoadProject succeeds and SinglePlan picks the plan up via BuildSchedule.
// The plan unit points to a prd.json with a single pre-passed story so the
// iteration loop completes immediately without needing agent marker output.
func projectFixture(t *testing.T, planID string) string {
	t.Helper()
	return projectFixtureOpts(t, planID, true)
}

// projectFixtureWithUnpassedStory writes a project fixture where the single
// story is NOT yet passed, so the iteration loop will invoke the agent.
func projectFixtureWithUnpassedStory(t *testing.T, planID string) string {
	t.Helper()
	return projectFixtureOpts(t, planID, false)
}

// projectFixtureOpts is the shared implementation for projectFixture variants.
// passed controls whether the single story is pre-marked as passed.
func projectFixtureOpts(t *testing.T, planID string, passed bool) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "springfield.toml"),
		[]byte("[project]\nagent_priority = [\"claude\"]\n"), 0o644); err != nil {
		t.Fatalf("toml: %v", err)
	}

	planSubDir := filepath.Join(root, ".springfield", "plans", planID)
	if err := os.MkdirAll(planSubDir, 0o755); err != nil {
		t.Fatalf("mkdir plan subdir: %v", err)
	}
	prdData := map[string]any{
		"id":    planID,
		"title": "Test Plan",
		"user_stories": []map[string]any{
			{"id": "US-001", "title": "Story", "passes": passed, "priority": 1, "deps": []string{}, "acceptance_criteria": []string{}},
		},
	}
	prdBytes, _ := json.MarshalIndent(prdData, "", "  ")
	prdPath := filepath.Join(planSubDir, "prd.json")
	if err := os.WriteFile(prdPath, prdBytes, 0o644); err != nil {
		t.Fatalf("write prd.json: %v", err)
	}

	cfg := map[string]any{
		"plans_dir":     ".springfield/plans",
		"worktree_base": ".worktrees",
		"max_retries":   1,
		"tool":          "claude",
		"plan_units": []map[string]any{
			{"id": planID, "path": ".springfield/plans/" + planID + "/prd.json", "order": 1},
		},
	}
	cfgPath := filepath.Join(root, ".springfield", "execution", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	return root
}

func TestSinglePlanRunsExactlyOneEligiblePlan(t *testing.T) {
	// Use unpassed story so the agent is actually dispatched once.
	root := projectFixtureWithUnpassedStory(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	// Add a second plan (beta) with its own prd.json to prove only one runs per call.
	betaDir := filepath.Join(root, ".springfield", "plans", "beta")
	if err := os.MkdirAll(betaDir, 0o755); err != nil {
		t.Fatalf("mkdir beta: %v", err)
	}
	betaPRD := map[string]any{
		"id": "beta", "title": "Beta",
		"user_stories": []map[string]any{
			{"id": "US-001", "title": "S", "passes": false, "priority": 1, "deps": []string{}, "acceptance_criteria": []string{}},
		},
	}
	betaBytes, _ := json.MarshalIndent(betaPRD, "", "  ")
	if err := os.WriteFile(filepath.Join(betaDir, "prd.json"), betaBytes, 0o644); err != nil {
		t.Fatalf("write beta prd: %v", err)
	}
	project.Config.PlanUnits = append(project.Config.PlanUnits, conductor.PlanUnit{
		ID: "beta", Path: ".springfield/plans/beta/prd.json", Order: 2,
	})
	if err := project.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	g := newFakeGit()
	// Agent emits story-pass marker so iteration loop can complete.
	runner := &fakeAgentRunner{}
	runner.events = []coreexec.Event{
		{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>"},
	}
	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
	})
	if res.Err != nil {
		t.Fatalf("SinglePlan: %v", res.Err)
	}
	if res.PlanID != "alpha" {
		t.Fatalf("ran wrong plan: %q", res.PlanID)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected exactly one agent dispatch, got %d", len(runner.calls))
	}
	if runner.calls[0].WorkDir != res.Context.WorktreeRoot {
		t.Fatalf("agent WorkDir not in worktree: WorkDir=%q WorktreeRoot=%q",
			runner.calls[0].WorkDir, res.Context.WorktreeRoot)
	}
}

func TestSinglePlanRecordsTruthfulCompletedState(t *testing.T) {
	root := projectFixture(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()
	runner := &fakeAgentRunner{}
	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
	})
	if res.Err != nil {
		t.Fatalf("SinglePlan: %v", res.Err)
	}

	// Re-load state from disk to prove persistence.
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	st, ok := reloaded.State.Plans["alpha"]
	if !ok {
		t.Fatalf("no state recorded for alpha")
	}
	if st.Status != conductor.StatusCompleted {
		t.Fatalf("status = %s, want completed", st.Status)
	}
	if st.WorktreePath == "" || st.Branch == "" || st.BaseRef == "" || st.BaseHead == "" {
		t.Fatalf("missing identity: %+v", st)
	}
	if st.InputDigest == "" {
		t.Fatalf("missing input digest")
	}
	if st.ExitReason != "completed" {
		t.Fatalf("exit reason = %q", st.ExitReason)
	}
	if st.EvidencePath == "" {
		t.Fatalf("missing evidence path")
	}
}

func TestSinglePlanRecordsFailureTruthfully(t *testing.T) {
	// Use unpassed story so agent is dispatched and can fail.
	root := projectFixtureWithUnpassedStory(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()
	runner := &fakeAgentRunner{failure: true}
	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
	})
	if res.Err == nil {
		t.Fatalf("expected failure result")
	}
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-load: %v", err)
	}
	st := reloaded.State.Plans["alpha"]
	if st == nil || st.Status != conductor.StatusFailed {
		t.Fatalf("expected failed state, got %+v", st)
	}
	if st.ExitReason != "agent-failed" {
		t.Fatalf("exit reason = %q", st.ExitReason)
	}
}

func TestSinglePlanReturnsEmptyResultWhenAllDone(t *testing.T) {
	root := projectFixture(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{Status: conductor.StatusCompleted}
	if err := project.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	g := newFakeGit()
	runner := &fakeAgentRunner{}
	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
	})
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.PlanID != "" || res.Reason != "no-eligible-plan" {
		t.Fatalf("expected no-eligible-plan, got %+v", res)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("agent must not be dispatched when nothing eligible")
	}
}

func TestSinglePlanReasonReflectsAgentFailure(t *testing.T) {
	root := projectFixtureWithUnpassedStory(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()
	runner := &fakeAgentRunner{failure: true}
	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
	})
	if res.Err == nil {
		t.Fatalf("expected agent failure")
	}
	if res.Reason != "agent-failed" {
		t.Fatalf("Reason = %q, want agent-failed (post-dispatch tag must override setup tag)", res.Reason)
	}
}

func TestSinglePlanReportsTerminalSaveFailureOnAgentSuccess(t *testing.T) {
	// Need unpassed story so agent runs; sabotage happens in beforeReply.
	// Agent emits story-pass + COMPLETE so the iteration completes normally,
	// then save fails.
	root := projectFixtureWithUnpassedStory(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()
	runner := &fakeAgentRunner{
		beforeReply: func() { sabotageStateJSON(t, root) },
		events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>"},
		},
	}
	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
	})
	if res.Err == nil {
		t.Fatalf("expected save-failure error to surface even after agent success")
	}
	if res.Reason != "state-save-failed" {
		t.Fatalf("Reason = %q, want state-save-failed", res.Reason)
	}
	if res.Status != conductor.StatusFailed {
		t.Fatalf("Status = %v, want failed", res.Status)
	}
	if !strings.Contains(res.Err.Error(), "save state") {
		t.Fatalf("error must mention save state: %v", res.Err)
	}
}

func TestSinglePlanReportsTerminalSaveFailureOnAgentFailure(t *testing.T) {
	root := projectFixtureWithUnpassedStory(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()
	runner := &fakeAgentRunner{
		failure:     true,
		beforeReply: func() { sabotageStateJSON(t, root) },
	}
	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
	})
	if res.Err == nil {
		t.Fatalf("expected combined agent + save error")
	}
	if res.Reason != "agent-failed-state-save-failed" {
		t.Fatalf("Reason = %q, want agent-failed-state-save-failed", res.Reason)
	}
	msg := res.Err.Error()
	if !strings.Contains(msg, "save state") {
		t.Fatalf("error must mention save state: %v", res.Err)
	}
	if !strings.Contains(msg, "agent") {
		t.Fatalf("error must preserve agent failure: %v", res.Err)
	}
}

func TestSinglePlanRecordsPreflightFailureWithoutDispatch(t *testing.T) {
	root := projectFixture(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()
	g.dirty = true
	runner := &fakeAgentRunner{}
	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
	})
	if res.Err == nil {
		t.Fatalf("expected dirty-source failure")
	}
	if res.Reason != "preflight-dirty-source" {
		t.Fatalf("Reason: %q", res.Reason)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("agent must not run on preflight failure")
	}
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-load: %v", err)
	}
	st := reloaded.State.Plans["alpha"]
	if st == nil || st.ExitReason != "preflight-dirty-source" {
		t.Fatalf("preflight tag not persisted: %+v", st)
	}
}

// TestTargetPlanIDOverridesDefaultSelection verifies that when TargetPlanID is
// set, SinglePlan dispatches that specific eligible plan instead of whichever
// plan would be next[0] from the schedule. The test sets TargetPlanID="alpha"
// on a project where "alpha" is the sole eligible plan; the critical check is
// that the field is honoured (no "no-eligible-plan" return, correct PlanID).
// A companion sub-test asserts that TargetPlanID for an ineligible (already-
// completed) plan returns "no-eligible-plan" rather than an unexpected run.
// projectFixtureLegacy writes a project where the plan unit points to a .md
// file (legacy path) instead of prd.json. The plan file at sourcePath is also
// written so buildPrompt can read it.
func projectFixtureLegacy(t *testing.T, planID string) (root, sourcePath string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "springfield.toml"),
		[]byte("[project]\nagent_priority = [\"claude\"]\n"), 0o644); err != nil {
		t.Fatalf("toml: %v", err)
	}

	planSubDir := filepath.Join(root, ".springfield", "plans", planID)
	if err := os.MkdirAll(planSubDir, 0o755); err != nil {
		t.Fatalf("mkdir plan subdir: %v", err)
	}
	// Write the legacy .md plan file.
	sourcePath = filepath.Join(planSubDir, "source.md")
	if err := os.WriteFile(sourcePath, []byte("# Plan\n\nDo the thing.\n"), 0o644); err != nil {
		t.Fatalf("write source.md: %v", err)
	}

	legacyPath := ".springfield/plans/" + planID + "/source.md"
	cfg := map[string]any{
		"plans_dir":     ".springfield/plans",
		"worktree_base": ".worktrees",
		"max_retries":   1,
		"tool":          "claude",
		"plan_units": []map[string]any{
			{"id": planID, "path": legacyPath, "order": 1},
		},
	}
	cfgPath := filepath.Join(root, ".springfield", "execution", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	return root, sourcePath
}

// tamperLegacyRunner writes to the legacy source.md file mid-run to simulate
// an agent tampering with the control plane.
type tamperLegacyRunner struct {
	sourcePath string
	calls      int
}

func (r *tamperLegacyRunner) Run(_ context.Context, req coreruntime.Request) coreruntime.Result {
	r.calls++
	_ = os.WriteFile(r.sourcePath, []byte("TAMPERED"), 0o644)
	return coreruntime.Result{
		Agent:     agents.AgentClaude,
		Status:    coreruntime.StatusPassed,
		ExitCode:  0,
		StartedAt: time.Now().Add(-time.Second),
		EndedAt:   time.Now(),
	}
}

// TestSinglePlanLegacyTamperGuardDetectsTamper verifies that TamperGuard is
// applied to legacy (.md) plans: an agent that modifies the source.md file is
// detected, the plan is marked failed, and source.md is restored.
func TestSinglePlanLegacyTamperGuardDetectsTamper(t *testing.T) {
	planID := "alpha"
	root, sourcePath := projectFixtureLegacy(t, planID)

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}

	origBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read source.md: %v", err)
	}

	guard := &spyTamperGuard{
		prdPath:    sourcePath,
		beforeData: origBytes,
	}
	agentRunner := &tamperLegacyRunner{sourcePath: sourcePath}
	g := newFakeGit()

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       agentRunner,
		Manager:      &planrun.Manager{Git: g},
		TamperGuard:  guard,
	})

	if res.Err == nil {
		t.Fatal("expected failure when legacy plan tamper detected")
	}
	if !strings.Contains(res.Err.Error(), "tamper") {
		t.Fatalf("error should mention tamper, got: %v", res.Err)
	}
	if res.Status != conductor.StatusFailed {
		t.Fatalf("status = %s, want failed", res.Status)
	}

	// Snapshot and Detect must have been called.
	if guard.snapshots == 0 {
		t.Error("expected Snapshot to be called")
	}
	if guard.detects == 0 {
		t.Error("expected Detect to be called")
	}
	// Restore must have been called.
	if guard.restores == 0 {
		t.Error("expected Restore to be called after tamper detected")
	}

	// source.md must be restored to original content.
	restored, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read restored source.md: %v", err)
	}
	if string(restored) != string(origBytes) {
		t.Errorf("source.md not restored: got %q, want %q", string(restored), string(origBytes))
	}

	// ExitReason must mention tamper.
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	st := reloaded.State.Plans[planID]
	if st == nil {
		t.Fatal("expected plan state to be saved")
	}
	if !strings.Contains(st.ExitReason, "tamper") {
		t.Errorf("ExitReason should mention tamper, got %q", st.ExitReason)
	}
}

func TestTargetPlanIDOverridesDefaultSelection(t *testing.T) {
	t.Run("eligible target is dispatched", func(t *testing.T) {
		// "alpha" is the only plan and is eligible.
		root := projectFixtureWithUnpassedStory(t, "alpha")
		project, err := conductor.LoadProject(root)
		if err != nil {
			t.Fatalf("LoadProject: %v", err)
		}

		g := newFakeGit()
		runner := &fakeAgentRunner{
			events: []coreexec.Event{
				{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>"},
			},
		}

		res := planrun.SinglePlan(planrun.SinglePlanInput{
			Project:      project,
			ControlRoot:  root,
			WorktreeBase: ".worktrees",
			AgentIDs:     []agents.ID{agents.AgentClaude},
			Runner:       runner,
			Manager:      &planrun.Manager{Git: g},
			TargetPlanID: "alpha",
		})

		if res.Err != nil {
			t.Fatalf("SinglePlan: %v", res.Err)
		}
		if res.PlanID != "alpha" {
			t.Fatalf("dispatched %q, want alpha", res.PlanID)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("expected 1 agent call, got %d", len(runner.calls))
		}
	})

	t.Run("ineligible target returns no-eligible-plan", func(t *testing.T) {
		// "alpha" is already completed+merged → not in NextPlans.
		root := projectFixture(t, "alpha")
		project, err := conductor.LoadProject(root)
		if err != nil {
			t.Fatalf("LoadProject: %v", err)
		}
		// Mark alpha as integrated so NextPlans returns nothing.
		// IsIntegrated requires Completed + MergeSucceeded + Cleanup != nil.
		project.State.Plans["alpha"] = &conductor.PlanState{
			Status:  conductor.StatusCompleted,
			Merge:   &conductor.MergeOutcome{Status: conductor.MergeSucceeded},
			Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
		}
		if err := project.SaveState(); err != nil {
			t.Fatalf("SaveState: %v", err)
		}

		g := newFakeGit()
		runner := &fakeAgentRunner{}

		res := planrun.SinglePlan(planrun.SinglePlanInput{
			Project:      project,
			ControlRoot:  root,
			WorktreeBase: ".worktrees",
			AgentIDs:     []agents.ID{agents.AgentClaude},
			Runner:       runner,
			Manager:      &planrun.Manager{Git: g},
			TargetPlanID: "alpha",
		})

		if res.Err != nil {
			t.Fatalf("unexpected err: %v", res.Err)
		}
		if res.Reason != "no-eligible-plan" {
			t.Fatalf("Reason = %q, want no-eligible-plan", res.Reason)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("agent must not be dispatched for ineligible target")
		}
	})

	t.Run("target not in NextPlans but pending is still dispatched", func(t *testing.T) {
		// Set up two plans: "unblocking" (order=1) and "target" (order=2).
		// "unblocking" is not yet integrated, so NextPlans returns only ["unblocking"].
		// TargetPlanID="target" — under the old schedule-eligibility check this
		// would return no-eligible-plan; under the new direct-lookup it must dispatch.
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "springfield.toml"),
			[]byte("[project]\nagent_priority = [\"claude\"]\n"), 0o644); err != nil {
			t.Fatalf("toml: %v", err)
		}

		// Write prd.json for both plans.
		for _, id := range []string{"unblocking", "target"} {
			planDir := filepath.Join(root, ".springfield", "plans", id)
			if err := os.MkdirAll(planDir, 0o755); err != nil {
				t.Fatalf("mkdir plan dir: %v", err)
			}
			prdData := map[string]any{
				"id":    id,
				"title": id,
				"user_stories": []map[string]any{
					{"id": "US-001", "title": "S", "passes": false, "priority": 1,
						"deps": []string{}, "acceptance_criteria": []string{}},
				},
			}
			prdBytes, _ := json.MarshalIndent(prdData, "", "  ")
			if err := os.WriteFile(filepath.Join(planDir, "prd.json"), prdBytes, 0o644); err != nil {
				t.Fatalf("write prd.json: %v", err)
			}
		}

		// Register both plans: unblocking(order=1), target(order=2).
		cfg := map[string]any{
			"plans_dir":     ".springfield/plans",
			"worktree_base": ".worktrees",
			"max_retries":   1,
			"tool":          "claude",
			"plan_units": []map[string]any{
				{"id": "unblocking", "path": ".springfield/plans/unblocking/prd.json", "order": 1},
				{"id": "target", "path": ".springfield/plans/target/prd.json", "order": 2},
			},
		}
		cfgPath := filepath.Join(root, ".springfield", "execution", "config.json")
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
			t.Fatalf("mkdir cfg: %v", err)
		}
		cfgData, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(cfgPath, cfgData, 0o644); err != nil {
			t.Fatalf("write cfg: %v", err)
		}

		project, err := conductor.LoadProject(root)
		if err != nil {
			t.Fatalf("LoadProject: %v", err)
		}
		// "unblocking" is still pending — so NextPlans returns ["unblocking"], not ["target"].
		// "target" is NOT in NextPlans but IS registered and pending.

		g := newFakeGit()
		runner := &fakeAgentRunner{
			events: []coreexec.Event{
				{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>"},
			},
		}

		res := planrun.SinglePlan(planrun.SinglePlanInput{
			Project:      project,
			ControlRoot:  root,
			WorktreeBase: ".worktrees",
			AgentIDs:     []agents.ID{agents.AgentClaude},
			Runner:       runner,
			Manager:      &planrun.Manager{Git: g},
			TargetPlanID: "target",
		})

		if res.Err != nil {
			t.Fatalf("SinglePlan: %v", res.Err)
		}
		if res.PlanID != "target" {
			t.Fatalf("dispatched %q, want target", res.PlanID)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("expected 1 agent call, got %d", len(runner.calls))
		}
	})
}

// queuedAgentRunner returns successive coreruntime.Results from a queue, one per
// Run call. Used to script story → review → fix sequences without inventing a
// new fake harness shape.
type queuedAgentRunner struct {
	results []coreruntime.Result
	calls   []coreruntime.Request
}

func (q *queuedAgentRunner) Run(_ context.Context, req coreruntime.Request) coreruntime.Result {
	q.calls = append(q.calls, req)
	if len(q.calls) > len(q.results) {
		// Out of scripted results — return a generic success with no events so
		// any unexpected extra call surfaces as a test failure downstream rather
		// than a panic here.
		return coreruntime.Result{Agent: agents.AgentClaude, Status: coreruntime.StatusPassed}
	}
	return q.results[len(q.calls)-1]
}

// TestSinglePlanReviewHaltYieldsNeedsHuman exercises the wiring: when
// ReviewConfig.Enabled=true and the reviewer emits <review-verdict>halt</…>,
// the plan terminates as StatusNeedsHuman with a non-nil error and NO pending
// merge — so Integrate never runs and the worktree/branch are preserved.
func TestSinglePlanReviewHaltYieldsNeedsHuman(t *testing.T) {
	root := projectFixtureWithUnpassedStory(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()

	// Call 1: story prompt → mark passed + COMPLETE.
	// Call 2: review prompt → halt verdict.
	runner := &queuedAgentRunner{results: []coreruntime.Result{
		{
			Agent:  agents.AgentClaude,
			Status: coreruntime.StatusPassed,
			Events: []coreexec.Event{{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>"}},
		},
		{
			Agent:  agents.AgentClaude,
			Status: coreruntime.StatusPassed,
			Events: []coreexec.Event{{Type: coreexec.EventStdout, Data: "<review-verdict>halt</review-verdict>"}},
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
	})

	if res.Status != conductor.StatusNeedsHuman {
		t.Fatalf("Status = %v, want StatusNeedsHuman", res.Status)
	}
	if res.Err == nil {
		t.Fatalf("Err must be non-nil so the batch loop halts; got nil")
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected exactly 2 agent calls (story + review halt), got %d", len(runner.calls))
	}

	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	st := reloaded.State.Plans["alpha"]
	if st == nil {
		t.Fatalf("no persisted state for alpha")
	}
	if st.Status != conductor.StatusNeedsHuman {
		t.Fatalf("persisted Status = %v, want StatusNeedsHuman", st.Status)
	}
	if st.Merge != nil {
		t.Fatalf("Merge must be nil on needs-human (Integrate must not run); got %+v", st.Merge)
	}

	summaryPath := filepath.Join(res.EvidencePath, "summary.json")
	data, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("read summary.json: %v", err)
	}
	if !strings.Contains(string(data), `"terminal_status": "needs-human"`) {
		t.Fatalf("summary.json must record needs-human, got:\n%s", string(data))
	}
}
