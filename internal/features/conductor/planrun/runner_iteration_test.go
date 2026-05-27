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

	// Check summary.json has iteration_count == 3 (actual, not cap).
	summaryPath := filepath.Join(evidenceDir, "summary.json")
	summaryData, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("read summary.json: %v", err)
	}
	var summary struct {
		IterationCount int    `json:"iteration_count"`
		TerminalStatus string `json:"terminal_status"`
	}
	if err := json.Unmarshal(summaryData, &summary); err != nil {
		t.Fatalf("unmarshal summary.json: %v", err)
	}
	if summary.IterationCount != 3 {
		t.Errorf("summary.json iteration_count = %d, want 3", summary.IterationCount)
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

func TestSinglePlanIterationOffTargetIDIgnoredWithWarning(t *testing.T) {
	// Agent emits a story-pass for US-099 which is neither in the PRD nor the
	// current iteration target. The marker must be silently ignored (warn in
	// progress.md), and the plan must fail via iteration cap (not a hard error
	// about the unknown ID). The old behavior called MarkPassed unconditionally,
	// which returned an error for unknown IDs; the new behavior filters by
	// story.ID first so MarkPassed is never called for off-target markers.
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
		},
	}

	root, project := projectFixtureWithPRD(t, "feat", p)
	project.Config.SingleWorkstreamIterations = 1
	if err := project.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

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
				StartedAt: time.Now().Add(-time.Second),
				EndedAt:   time.Now(),
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

	// Plan must fail (iteration cap) — the off-target marker is ignored, not a hard error.
	if res.Err == nil {
		t.Fatal("expected failure (iteration cap), not success")
	}
	if !strings.Contains(res.Err.Error(), "iteration cap") {
		t.Fatalf("expected iteration cap error, got: %v", res.Err)
	}

	// US-001 must remain unpassed on disk.
	prdPath := filepath.Join(root, ".springfield", "plans", "feat", "prd.json")
	finalPRD, err := prd.ParseFile(prdPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if finalPRD.UserStories[0].Passes {
		t.Error("US-001 must not be marked passed when agent only emitted off-target US-099")
	}

	// progress.md must contain a WARN about the off-target marker.
	progressPath := filepath.Join(root, ".springfield", "plans", "feat", "progress.md")
	progressData, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatalf("read progress.md: %v", err)
	}
	if !strings.Contains(string(progressData), "WARN") {
		t.Errorf("expected WARN in progress.md for off-target story-pass, got:\n%s", progressData)
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

func TestSinglePlanIterationAgentFailureExitCodeZero(t *testing.T) {
	// Agent with StatusFailed but ExitCode 0 must still cause plan failure and
	// abort the loop after 1 iteration.
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
				ExitCode: 0,
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
		t.Fatal("expected failure when agent status is StatusFailed (even with ExitCode 0)")
	}
	if res.Status != conductor.StatusFailed {
		t.Fatalf("status = %s, want failed", res.Status)
	}
	if runner.calls != 1 {
		t.Fatalf("expected loop to abort after 1 iter, got %d calls", runner.calls)
	}
}

// tamperAgentRunner is an agent runner that writes to prd.json mid-iteration
// to simulate a tampered control plane.
type tamperAgentRunner struct {
	prdPath string
	calls   int
}

func (r *tamperAgentRunner) Run(_ context.Context, req coreruntime.Request) coreruntime.Result {
	r.calls++
	// Write unexpected content to prd.json to simulate tampering.
	_ = os.WriteFile(r.prdPath, []byte(`{"id":"tampered"}`), 0o644)
	return coreruntime.Result{
		Agent:    agents.AgentClaude,
		Status:   coreruntime.StatusPassed,
		ExitCode: 0,
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass>", Time: time.Now()},
		},
		StartedAt: time.Now().Add(-time.Second),
		EndedAt:   time.Now(),
	}
}

// spyTamperGuard implements planrun.TamperGuard and records calls.
// It simulates tamper detection by comparing a "before" snapshot of prdPath
// to the current bytes after the agent runs.
type spyTamperGuard struct {
	prdPath    string
	beforeData []byte
	snapshots  int
	detects    int
	restores   int
}

func (g *spyTamperGuard) Snapshot() error {
	g.snapshots++
	data, err := os.ReadFile(g.prdPath)
	if err != nil {
		return err
	}
	g.beforeData = data
	return nil
}

func (g *spyTamperGuard) Detect() (string, error) {
	g.detects++
	current, err := os.ReadFile(g.prdPath)
	if err != nil {
		return fmt.Sprintf("read error: %v", err), nil
	}
	if string(current) != string(g.beforeData) {
		return "prd.json changed", nil
	}
	return "", nil
}

func (g *spyTamperGuard) Restore() error {
	g.restores++
	return os.WriteFile(g.prdPath, g.beforeData, 0o644)
}

func TestSinglePlanIterationTamperGuardDetectsTamperAndFailsPlan(t *testing.T) {
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
		},
	}

	root, project := projectFixtureWithPRD(t, "feat", p)
	prdPath := filepath.Join(root, ".springfield", "plans", "feat", "prd.json")
	g := newFakeGit()

	// Read the original prd.json bytes for the guard.
	origBytes, err := os.ReadFile(prdPath)
	if err != nil {
		t.Fatalf("read prd.json: %v", err)
	}

	guard := &spyTamperGuard{
		prdPath:    prdPath,
		beforeData: origBytes,
	}
	agentRunner := &tamperAgentRunner{prdPath: prdPath}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       agentRunner,
		Manager:      &planrun.Manager{Git: g},
		ProjectRoot:  root,
		TamperGuard:  guard,
	})

	if res.Err == nil {
		t.Fatal("expected failure when tamper detected")
	}
	if !strings.Contains(res.Err.Error(), "tamper") {
		t.Fatalf("error should mention tamper, got: %v", res.Err)
	}
	if res.Status != conductor.StatusFailed {
		t.Fatalf("status = %s, want failed", res.Status)
	}

	// Guard Snapshot and Detect must have been called.
	if guard.snapshots == 0 {
		t.Error("expected Snapshot to be called at least once")
	}
	if guard.detects == 0 {
		t.Error("expected Detect to be called at least once")
	}
	// Restore must have been called to put prd.json back.
	if guard.restores == 0 {
		t.Error("expected Restore to be called after tamper detected")
	}

	// prd.json must be restored to original content.
	restored, err := os.ReadFile(prdPath)
	if err != nil {
		t.Fatalf("read restored prd.json: %v", err)
	}
	if string(restored) != string(origBytes) {
		t.Errorf("prd.json not restored: got %q, want %q", string(restored), string(origBytes))
	}

	// Exit reason must contain tamper.
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	st := reloaded.State.Plans["feat"]
	if st == nil {
		t.Fatal("expected plan state to be saved")
	}
	if !strings.Contains(st.ExitReason, "tamper") {
		t.Errorf("ExitReason should mention tamper, got %q", st.ExitReason)
	}
}

func TestSinglePlanIterationTamperGuardNoOpWhenNoTamper(t *testing.T) {
	// Guard with no tamper detected — plan completes normally.
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
		},
	}

	root, project := projectFixtureWithPRD(t, "feat", p)
	prdPath := filepath.Join(root, ".springfield", "plans", "feat", "prd.json")
	g := newFakeGit()

	origBytes, err := os.ReadFile(prdPath)
	if err != nil {
		t.Fatalf("read prd.json: %v", err)
	}

	// Guard that always reports no tamper.
	guard := &spyTamperGuard{
		prdPath:    prdPath,
		beforeData: origBytes,
	}

	runner := &iterScriptRunner{
		replies: []coreruntime.Result{
			makePassAndCompleteResult("US-001"),
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
		TamperGuard:  guard,
	})

	if res.Err != nil {
		t.Fatalf("SinglePlan with no-tamper guard should succeed: %v", res.Err)
	}
	if res.Status != conductor.StatusCompleted {
		t.Fatalf("status = %s, want completed", res.Status)
	}
	if guard.restores != 0 {
		t.Errorf("Restore must not be called when no tamper, got %d calls", guard.restores)
	}
}

func TestSinglePlanIterationCyclicDepsBlockedFailsWithNoAgentCall(t *testing.T) {
	// US-001 deps US-002, US-002 deps US-001 → cycle; US-003 already passed.
	// Plan must fail immediately with blocked exit reason; no agent invocation.
	p := prd.PRD{
		ID:    "feat",
		Title: "Cyclic Dep Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false, Deps: []string{"US-002"}},
			{ID: "US-002", Title: "Story 2", Priority: 2, Passes: false, Deps: []string{"US-001"}},
			{ID: "US-003", Title: "Story 3", Priority: 3, Passes: true},
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

	if res.Err == nil {
		t.Fatal("expected failure for cyclic dep graph")
	}
	if !strings.Contains(res.Err.Error(), "blocked") {
		t.Fatalf("error should mention 'blocked', got: %v", res.Err)
	}
	if res.Status != conductor.StatusFailed {
		t.Fatalf("status = %s, want failed", res.Status)
	}
	if runner.calls != 0 {
		t.Fatalf("expected no agent calls for blocked plan, got %d", runner.calls)
	}
}

func TestSinglePlanIterationZeroStoryPRDFails(t *testing.T) {
	// Zero-story PRD written by direct file edit (bypassing Validate) must fail
	// with a clear exit reason. The runtime guard catches it so a manually-crafted
	// prd.json with empty user_stories never silently succeeds.
	p := prd.PRD{
		ID:          "feat",
		Title:       "Zero Story Plan",
		UserStories: []prd.UserStory{},
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

	if res.Err == nil {
		t.Fatal("expected SinglePlan to fail for zero-story PRD")
	}
	if res.Status != conductor.StatusFailed {
		t.Fatalf("status = %s, want failed", res.Status)
	}
	if runner.calls != 0 {
		t.Fatalf("expected no agent calls for zero-story plan, got %d", runner.calls)
	}
	if !strings.Contains(res.Err.Error(), "zero user stories") {
		t.Fatalf("error should mention 'zero user stories', got: %v", res.Err)
	}
}

func TestSinglePlanIterationAppendProgressNonFatal(t *testing.T) {
	// progress.md read-only must not cause plan failure — loop continues.
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
			makePassAndCompleteResult("US-001"),
		},
	}

	// Create progress.md as read-only before running.
	progressPath := filepath.Join(root, ".springfield", "plans", "feat", "progress.md")
	if err := os.MkdirAll(filepath.Dir(progressPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(progressPath, []byte("existing\n"), 0o444); err != nil {
		t.Fatalf("write progress read-only: %v", err)
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

	// AppendProgress errors must not propagate as plan failure.
	if res.Err != nil {
		t.Fatalf("SinglePlan failed despite read-only progress.md: %v", res.Err)
	}
	if res.Status != conductor.StatusCompleted {
		t.Fatalf("status = %s, want completed", res.Status)
	}
}

// TestSinglePlanIterationRejectsOffTargetStoryPass verifies that when an agent
// emits a <story-pass> for a story that is NOT the current iteration target,
// the marker is ignored, a WARN is appended to progress.md, and the next
// iteration still targets the original story (no permanent skip).
func TestSinglePlanIterationRejectsOffTargetStoryPass(t *testing.T) {
	// US-001 is the target (priority 1). Agent emits pass for US-002 instead.
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
			{ID: "US-002", Title: "Story 2", Priority: 2, Passes: false},
		},
	}

	root, project := projectFixtureWithPRD(t, "feat", p)
	project.Config.SingleWorkstreamIterations = 2
	if err := project.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	g := newFakeGit()
	// Iter 1: agent emits only US-002 (off target, should be ignored).
	// Iter 2: exhausts cap.
	runner := &iterScriptRunner{
		replies: []coreruntime.Result{
			{
				Agent:    agents.AgentClaude,
				Status:   coreruntime.StatusPassed,
				ExitCode: 0,
				Events: []coreexec.Event{
					{Type: coreexec.EventStdout, Data: "<story-pass>US-002</story-pass>", Time: time.Now()},
				},
				StartedAt: time.Now().Add(-time.Second),
				EndedAt:   time.Now(),
			},
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

	// Plan must fail (iteration cap) — not complete.
	if res.Err == nil {
		t.Fatal("expected failure (iteration cap), not success")
	}

	// US-002 must NOT be marked passed on disk.
	prdPath := filepath.Join(root, ".springfield", "plans", "feat", "prd.json")
	finalPRD, err := prd.ParseFile(prdPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	for _, s := range finalPRD.UserStories {
		if s.ID == "US-002" && s.Passes {
			t.Errorf("US-002 must not be marked passed when it was not the iteration target")
		}
		if s.ID == "US-001" && s.Passes {
			t.Errorf("US-001 must not be marked passed (agent never passed it)")
		}
	}

	// progress.md must contain a WARN about the rejected off-target marker.
	progressPath := filepath.Join(root, ".springfield", "plans", "feat", "progress.md")
	progressData, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatalf("read progress.md: %v", err)
	}
	if !strings.Contains(string(progressData), "WARN") {
		t.Errorf("expected WARN in progress.md for off-target story-pass, got:\n%s", progressData)
	}
	if !strings.Contains(string(progressData), "US-002") {
		t.Errorf("expected WARN to mention US-002, got:\n%s", progressData)
	}
}

// TestSinglePlanIterationCompletedCountReportsHonoredPasses verifies that the
// "iteration N completed (passed=X ...)" line reports the number of pass markers
// actually HONORED, not the raw count scanned from agent output. An agent that
// emits off-target markers (which are ignored) must not inflate the count — the
// completed line would otherwise contradict the WARN lines directly above it.
func TestSinglePlanIterationCompletedCountReportsHonoredPasses(t *testing.T) {
	// US-001 is the target. Agent emits passes for US-002 and US-003 (both
	// off target, both ignored). Honored count must be 0.
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
	project.Config.SingleWorkstreamIterations = 1
	if err := project.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	g := newFakeGit()
	runner := &iterScriptRunner{
		replies: []coreruntime.Result{
			{
				Agent:    agents.AgentClaude,
				Status:   coreruntime.StatusPassed,
				ExitCode: 0,
				Events: []coreexec.Event{
					{Type: coreexec.EventStdout,
						Data: "<story-pass>US-002</story-pass><story-pass>US-003</story-pass>",
						Time: time.Now()},
				},
				StartedAt: time.Now().Add(-time.Second),
				EndedAt:   time.Now(),
			},
		},
	}

	planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
		ProjectRoot:  root,
	})

	progressPath := filepath.Join(root, ".springfield", "plans", "feat", "progress.md")
	progressData, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatalf("read progress.md: %v", err)
	}
	content := string(progressData)

	// Both off-target markers were scanned (2) but none honored. The completed
	// line must report passed=0, not passed=2. Match the trailing " complete="
	// so the count digit is anchored (avoids passed=0 matching passed=0X etc.).
	if !strings.Contains(content, "iteration 1 completed (passed=0 complete=") {
		t.Errorf("expected completed line to report honored count passed=0, got:\n%s", content)
	}
	if strings.Contains(content, "iteration 1 completed (passed=2 complete=") {
		t.Errorf("completed line reported scanned count (passed=2) instead of honored count, got:\n%s", content)
	}
}

// TestSinglePlanIterationCurrentAndOffTargetPass verifies that when an agent
// emits both the current target's <story-pass> AND a wrong story's <story-pass>,
// only the current target is marked; the off-target is silently warned in progress.md.
func TestSinglePlanIterationCurrentAndOffTargetPass(t *testing.T) {
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
	// Agent emits US-001 (current target) + US-002 (off target) + COMPLETE.
	// US-001 should be marked passed; US-002 should not.
	// After US-001 is passed, next target is US-002. Agent must be called again.
	// Iter 2: pass US-002 + COMPLETE.
	runner := &iterScriptRunner{
		replies: []coreruntime.Result{
			{
				Agent:    agents.AgentClaude,
				Status:   coreruntime.StatusPassed,
				ExitCode: 0,
				Events: []coreexec.Event{
					{Type: coreexec.EventStdout,
						Data: "<story-pass>US-001</story-pass><story-pass>US-002</story-pass><promise>COMPLETE</promise>",
						Time: time.Now()},
				},
				StartedAt: time.Now().Add(-time.Second),
				EndedAt:   time.Now(),
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
	if res.Status != conductor.StatusCompleted {
		t.Fatalf("status = %s, want completed", res.Status)
	}
	if runner.calls != 2 {
		t.Fatalf("expected 2 agent calls, got %d", runner.calls)
	}

	// progress.md must warn about US-002 being rejected in iter 1.
	progressPath := filepath.Join(root, ".springfield", "plans", "feat", "progress.md")
	progressData, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatalf("read progress.md: %v", err)
	}
	content := string(progressData)
	if !strings.Contains(content, "WARN") {
		t.Errorf("expected WARN in progress.md for off-target US-002, got:\n%s", content)
	}
	if !strings.Contains(content, "US-002") {
		t.Errorf("WARN should mention US-002, got:\n%s", content)
	}
	// Positive-path count: iter 1 honored US-001 (target) and rejected US-002
	// (off target), so the completed line must report passed=1 — proving the
	// honored counter increments for a real pass, not just that it stays 0.
	if !strings.Contains(content, "iteration 1 completed (passed=1 complete=") {
		t.Errorf("expected iter 1 completed line to report honored count passed=1, got:\n%s", content)
	}
}
