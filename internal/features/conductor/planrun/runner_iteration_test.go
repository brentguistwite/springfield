package planrun_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planrun"
	"springfield/internal/features/prd"
)

// iterScriptRunner is a scripted AgentRunner for iteration tests.
// Each call pops the next Reply from replies slice.
type iterScriptRunner struct {
	replies []coreruntime.Result
	calls   int
}

func (r *iterScriptRunner) Run(_ context.Context, req coreruntime.Request) coreruntime.Result {
	r.calls++
	if r.calls > len(r.replies) {
		// Return a failed result if over-called.
		return coreruntime.Result{
			Agent:    agents.AgentClaude,
			Status:   coreruntime.StatusFailed,
			ExitCode: 1,
		}
	}
	return r.replies[r.calls-1]
}

func makePassResult(storyID string) coreruntime.Result {
	return coreruntime.Result{
		Agent:    agents.AgentClaude,
		Status:   coreruntime.StatusPassed,
		ExitCode: 0,
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: fmt.Sprintf("<story-pass>%s</story-pass>", storyID), Time: time.Now()},
		},
		StartedAt: time.Now().Add(-time.Second),
		EndedAt:   time.Now(),
	}
}

func makePassAndCompleteResult(storyID string) coreruntime.Result {
	return coreruntime.Result{
		Agent:    agents.AgentClaude,
		Status:   coreruntime.StatusPassed,
		ExitCode: 0,
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: fmt.Sprintf("<story-pass>%s</story-pass><promise>COMPLETE</promise>", storyID), Time: time.Now()},
		},
		StartedAt: time.Now().Add(-time.Second),
		EndedAt:   time.Now(),
	}
}

func makeNoMarkerResult() coreruntime.Result {
	return coreruntime.Result{
		Agent:    agents.AgentClaude,
		Status:   coreruntime.StatusPassed,
		ExitCode: 0,
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: "just some text without markers", Time: time.Now()},
		},
		StartedAt: time.Now().Add(-time.Second),
		EndedAt:   time.Now(),
	}
}

// prdFixture writes a prd.json and its parent dirs to the given root under
// the path .springfield/plans/<planID>/prd.json, returning the path.
func prdFixture(t *testing.T, root, planID string, p prd.PRD) string {
	t.Helper()
	prdPath := filepath.Join(root, ".springfield", "plans", planID, "prd.json")
	if err := os.MkdirAll(filepath.Dir(prdPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatalf("marshal prd: %v", err)
	}
	if err := os.WriteFile(prdPath, data, 0o644); err != nil {
		t.Fatalf("write prd: %v", err)
	}
	return prdPath
}

// projectFixtureWithPRD builds a project where the plan unit points to prd.json.
func projectFixtureWithPRD(t *testing.T, planID string, p prd.PRD) (string, *conductor.Project) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "springfield.toml"),
		[]byte("[project]\nagent_priority = [\"claude\"]\n"), 0o644); err != nil {
		t.Fatalf("toml: %v", err)
	}

	prdPath := prdFixture(t, root, planID, p)
	// Also write a dummy plan file for InputDigest (the plan path must exist).
	// We reuse the prd.json path itself as the plan path.
	_ = prdPath

	cfg := map[string]any{
		"plans_dir":                    ".springfield/plans",
		"worktree_base":                ".worktrees",
		"max_retries":                  1,
		"single_workstream_iterations": 10,
		"tool":                         "claude",
		"plan_units": []map[string]any{
			{
				"id":    planID,
				"path":  ".springfield/plans/" + planID + "/prd.json",
				"order": 1,
			},
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

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	return root, project
}

func TestSinglePlanIterationThreeStoryFullPass(t *testing.T) {
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
			{ID: "US-002", Title: "Story 2", Priority: 2, Passes: false},
			{ID: "US-003", Title: "Story 3", Priority: 3, Passes: false},
		},
	}

	root, project := projectFixtureWithPRD(t, "feat", p)
	g := newFakeGit()
	runner := &iterScriptRunner{
		replies: []coreruntime.Result{
			makePassResult("US-001"),
			makePassResult("US-002"),
			makePassAndCompleteResult("US-003"),
		},
	}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
		ProjectRoot:  root,
	})

	if res.Err != nil {
		t.Fatalf("SinglePlan: %v", res.Err)
	}
	if res.Status != conductor.StatusCompleted {
		t.Fatalf("status = %s, want completed", res.Status)
	}
	if runner.calls != 3 {
		t.Fatalf("expected 3 agent calls, got %d", runner.calls)
	}

	// Check on-disk prd.json has all 3 passes=true.
	prdPath := filepath.Join(root, ".springfield", "plans", "feat", "prd.json")
	finalPRD, err := prd.ParseFile(prdPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	for _, s := range finalPRD.UserStories {
		if !s.Passes {
			t.Errorf("story %s should be passed, but passes=false", s.ID)
		}
	}

	// Check progress.md exists with entries.
	progressPath := filepath.Join(root, ".springfield", "plans", "feat", "progress.md")
	data, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatalf("read progress.md: %v", err)
	}
	content := string(data)
	// 3 start entries + 3 complete entries + 3 pass entries = at least 9 lines
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) < 9 {
		t.Errorf("expected at least 9 progress lines, got %d: %s", len(lines), content)
	}

	// Check per-iteration evidence at iter-1/, iter-2/, iter-3/.
	evidenceDir := planrun.EvidenceRoot(root, "feat")
	for _, iter := range []string{"iter-1", "iter-2", "iter-3"} {
		iterDir := filepath.Join(evidenceDir, iter)
		if _, err := os.Stat(iterDir); err != nil {
			t.Errorf("missing evidence dir %s: %v", iterDir, err)
		}
	}

	// Check summary.json.
	summaryPath := filepath.Join(evidenceDir, "summary.json")
	if _, err := os.Stat(summaryPath); err != nil {
		t.Errorf("missing summary.json: %v", err)
	}

	// Check MergePending is set.
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	st := reloaded.State.Plans["feat"]
	if st == nil || st.Merge == nil {
		t.Fatal("expected merge outcome to be set")
	}
	if st.Merge.Status != conductor.MergePending {
		t.Fatalf("merge status = %s, want pending", st.Merge.Status)
	}
}

func TestSinglePlanIterationCapExhaustion(t *testing.T) {
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
		},
	}

	root, project := projectFixtureWithPRD(t, "feat", p)
	// Set iteration cap to 3 via config.
	project.Config.SingleWorkstreamIterations = 3
	if err := project.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	g := newFakeGit()
	runner := &iterScriptRunner{
		replies: []coreruntime.Result{
			makeNoMarkerResult(),
			makeNoMarkerResult(),
			makeNoMarkerResult(),
		},
	}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
		ProjectRoot:  root,
	})

	if res.Err == nil {
		t.Fatal("expected failure on iteration cap exhaustion")
	}
	if !strings.Contains(res.Err.Error(), "iteration cap") {
		t.Fatalf("error should mention iteration cap, got: %v", res.Err)
	}
	if runner.calls != 3 {
		t.Fatalf("expected exactly 3 agent calls, got %d", runner.calls)
	}

	// prd.json should still have passes=false.
	prdPath := filepath.Join(root, ".springfield", "plans", "feat", "prd.json")
	finalPRD, err := prd.ParseFile(prdPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if finalPRD.UserStories[0].Passes {
		t.Error("story should not be passed when iteration cap reached")
	}

	// 3 iter-N evidence dirs should exist.
	evidenceDir := planrun.EvidenceRoot(root, "feat")
	for _, iter := range []string{"iter-1", "iter-2", "iter-3"} {
		iterDir := filepath.Join(evidenceDir, iter)
		if _, err := os.Stat(iterDir); err != nil {
			t.Errorf("missing evidence dir %s: %v", iterDir, err)
		}
	}
}

func TestSinglePlanIterationAlreadyCompleteNoPlanNoAgent(t *testing.T) {
	// All stories already passed — runner should NOT invoke agent, but should
	// set MergePending and status=completed.
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: true},
			{ID: "US-002", Title: "Story 2", Priority: 2, Passes: true},
		},
	}

	root, project := projectFixtureWithPRD(t, "feat", p)
	g := newFakeGit()
	runner := &iterScriptRunner{}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
		ProjectRoot:  root,
	})

	if res.Err != nil {
		t.Fatalf("SinglePlan: %v", res.Err)
	}
	if res.Status != conductor.StatusCompleted {
		t.Fatalf("status = %s, want completed", res.Status)
	}
	if runner.calls != 0 {
		t.Fatalf("expected no agent calls for already-complete plan, got %d", runner.calls)
	}

	// Still should have MergePending.
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	st := reloaded.State.Plans["feat"]
	if st == nil || st.Merge == nil || st.Merge.Status != conductor.MergePending {
		t.Fatalf("expected MergePending even for already-complete plan, got %+v", st)
	}
}

func TestSinglePlanIterationMarkPassedUnknownIDFails(t *testing.T) {
	// Agent emits a story-pass for US-099 which doesn't exist in the PRD.
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
		},
	}

	root, project := projectFixtureWithPRD(t, "feat", p)
	g := newFakeGit()
	runner := &iterScriptRunner{
		replies: []coreruntime.Result{
			{
				Agent:    agents.AgentClaude,
				Status:   coreruntime.StatusPassed,
				ExitCode: 0,
				Events: []coreexec.Event{
					{Type: coreexec.EventStdout, Data: "<story-pass>US-099</story-pass>", Time: time.Now()},
				},
			},
		},
	}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
		ProjectRoot:  root,
	})

	if res.Err == nil {
		t.Fatal("expected failure for unknown story ID")
	}
	if !strings.Contains(res.Err.Error(), "US-099") {
		t.Fatalf("error should mention unknown ID, got: %v", res.Err)
	}
}

func TestSinglePlanIterationCompleteWithStoriesPendingContinues(t *testing.T) {
	// Agent emits COMPLETE after only passing US-001, but US-002 still pending.
	// Loop should continue. Then in iter 2, pass US-002.
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
			{ID: "US-002", Title: "Story 2", Priority: 2, Passes: false},
		},
	}

	root, project := projectFixtureWithPRD(t, "feat", p)
	g := newFakeGit()
	// Iter 1: pass US-001 + premature COMPLETE
	// Iter 2: pass US-002 + COMPLETE (valid)
	runner := &iterScriptRunner{
		replies: []coreruntime.Result{
			{
				Agent:    agents.AgentClaude,
				Status:   coreruntime.StatusPassed,
				ExitCode: 0,
				Events: []coreexec.Event{
					{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>", Time: time.Now()},
				},
			},
			makePassAndCompleteResult("US-002"),
		},
	}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
		ProjectRoot:  root,
	})

	if res.Err != nil {
		t.Fatalf("SinglePlan: %v", res.Err)
	}
	if runner.calls != 2 {
		t.Fatalf("expected 2 iterations (premature COMPLETE ignored), got %d", runner.calls)
	}

	// Verify warning in progress.md.
	progressPath := filepath.Join(root, ".springfield", "plans", "feat", "progress.md")
	data, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatalf("read progress: %v", err)
	}
	if !strings.Contains(string(data), "WARN") {
		t.Errorf("expected WARN in progress.md for premature COMPLETE, content: %s", string(data))
	}
}

func TestSinglePlanIterationAgentFailure(t *testing.T) {
	// Agent returns StatusFailed — loop should abort, plan marked failed.
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
		},
	}

	root, project := projectFixtureWithPRD(t, "feat", p)
	g := newFakeGit()
	runner := &iterScriptRunner{
		replies: []coreruntime.Result{
			{
				Agent:    agents.AgentClaude,
				Status:   coreruntime.StatusFailed,
				ExitCode: 1,
			},
		},
	}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
		ProjectRoot:  root,
	})

	if res.Err == nil {
		t.Fatal("expected failure")
	}
	if res.Status != conductor.StatusFailed {
		t.Fatalf("status = %s, want failed", res.Status)
	}
}
