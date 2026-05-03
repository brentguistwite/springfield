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
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planrun"
)

// fakeAgentRunner is an in-memory AgentRunner for SinglePlan tests.
type fakeAgentRunner struct {
	calls       []coreruntime.Request
	result      coreruntime.Result
	failure     bool
	beforeReply func()
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
func projectFixture(t *testing.T, planID string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "springfield.toml"),
		[]byte("[project]\nagent_priority = [\"claude\"]\n"), 0o644); err != nil {
		t.Fatalf("toml: %v", err)
	}
	planDir := filepath.Join(root, "springfield", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}
	if err := os.WriteFile(filepath.Join(planDir, planID+".md"), []byte("# plan body\n"), 0o644); err != nil {
		t.Fatalf("plan body: %v", err)
	}
	cfg := map[string]any{
		"plans_dir":     "springfield/plans",
		"worktree_base": ".worktrees",
		"max_retries":   1,
		"tool":          "claude",
		"plan_units": []map[string]any{
			{"id": planID, "path": "springfield/plans/" + planID + ".md", "order": 1},
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
	root := projectFixture(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	// Add a second plan to prove only one runs per call.
	project.Config.PlanUnits = append(project.Config.PlanUnits, conductor.PlanUnit{
		ID: "beta", Path: "springfield/plans/alpha.md", Order: 2,
	})
	if err := project.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig: %v", err)
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
	root := projectFixture(t, "alpha")
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
	root := projectFixture(t, "alpha")
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
	root := projectFixture(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()
	runner := &fakeAgentRunner{
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
	root := projectFixture(t, "alpha")
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
