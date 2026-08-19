package planrun_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/core/agents"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planrun"
	"springfield/internal/features/portblock"
	"springfield/internal/features/prd"
)

// envCapturingRunner records the Env map handed to every agent dispatch so a
// test can prove the slice's port block reached the agent and stayed stable
// across iterations. It scripts the same replies in order like iterScriptRunner.
type envCapturingRunner struct {
	replies     []coreruntime.Result
	calls       int
	capturedEnv []map[string]string
}

func (r *envCapturingRunner) Run(_ context.Context, req coreruntime.Request) coreruntime.Result {
	r.capturedEnv = append(r.capturedEnv, req.Env)
	r.calls++
	if r.calls > len(r.replies) {
		return coreruntime.Result{Agent: agents.AgentClaude, Status: coreruntime.StatusFailed, ExitCode: 1}
	}
	return r.replies[r.calls-1]
}

// projectFixtureWithOrder is projectFixtureWithPRD with a caller-chosen 1-based
// plan-unit Order, so a test can pin the ordinal the port block derives from.
func projectFixtureWithOrder(t *testing.T, planID string, order int, p prd.PRD) (string, *conductor.Project) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "springfield.toml"),
		[]byte("[project]\nagent_priority = [\"claude\"]\n"), 0o644); err != nil {
		t.Fatalf("toml: %v", err)
	}
	prdFixture(t, root, planID, p)

	cfg := map[string]any{
		"plans_dir":                    ".springfield/plans",
		"worktree_base":                ".worktrees",
		"max_retries":                  1,
		"single_workstream_iterations": 10,
		"tool":                         "claude",
		"plan_units": []map[string]any{
			{"id": planID, "path": ".springfield/plans/" + planID + "/prd.json", "order": order},
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

// TestSinglePlanInjectsPortBlockDerivedFromOrdinal proves the agent receives the
// SPRINGFIELD_PORT/SPRINGFIELD_PORT_RANGE block for its plan's 1-based ordinal.
func TestSinglePlanInjectsPortBlockDerivedFromOrdinal(t *testing.T) {
	p := prd.PRD{ID: "feat", Title: "F", UserStories: []prd.UserStory{
		{ID: "US-001", Title: "S1", Priority: 1},
	}}
	root, project := projectFixtureWithOrder(t, "feat", 3, p)
	runner := &envCapturingRunner{replies: []coreruntime.Result{makePassAndCompleteResult("US-001")}}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: newFakeGit()},
		ProjectRoot:  root,
	})
	if res.Err != nil {
		t.Fatalf("SinglePlan: %v", res.Err)
	}
	if len(runner.capturedEnv) != 1 {
		t.Fatalf("expected 1 agent call, got %d", len(runner.capturedEnv))
	}
	want := portblock.Allocate(portblock.DefaultBase, 3).Env() // {42020, 42020-42029}
	got := runner.capturedEnv[0]
	if got[portblock.EnvPort] != want[portblock.EnvPort] {
		t.Errorf("%s = %q, want %q", portblock.EnvPort, got[portblock.EnvPort], want[portblock.EnvPort])
	}
	if got[portblock.EnvPortRange] != want[portblock.EnvPortRange] {
		t.Errorf("%s = %q, want %q", portblock.EnvPortRange, got[portblock.EnvPortRange], want[portblock.EnvPortRange])
	}
}

// TestSinglePlanPortBlockStableAcrossIterations proves a slice's block does not
// drift between iterations of the same run: a two-story plan dispatches the
// agent twice and both see the identical port env.
func TestSinglePlanPortBlockStableAcrossIterations(t *testing.T) {
	p := prd.PRD{ID: "feat", Title: "F", UserStories: []prd.UserStory{
		{ID: "US-001", Title: "S1", Priority: 1},
		{ID: "US-002", Title: "S2", Priority: 2},
	}}
	root, project := projectFixtureWithOrder(t, "feat", 1, p)
	runner := &envCapturingRunner{replies: []coreruntime.Result{
		makePassResult("US-001"),
		makePassAndCompleteResult("US-002"),
	}}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: newFakeGit()},
		ProjectRoot:  root,
	})
	if res.Err != nil {
		t.Fatalf("SinglePlan: %v", res.Err)
	}
	if len(runner.capturedEnv) != 2 {
		t.Fatalf("expected 2 agent calls, got %d", len(runner.capturedEnv))
	}
	a, b := runner.capturedEnv[0], runner.capturedEnv[1]
	if a[portblock.EnvPort] != b[portblock.EnvPort] || a[portblock.EnvPortRange] != b[portblock.EnvPortRange] {
		t.Fatalf("port env drifted between iterations: %v vs %v", a, b)
	}
	if a[portblock.EnvPort] != "42000" {
		t.Fatalf("ordinal 1 first port = %q, want 42000", a[portblock.EnvPort])
	}
}

// TestSinglePlanConcurrentSlicesGetDisjointBlocks proves two slices (distinct
// ordinals) receive non-overlapping port env — the parallel-safety guarantee at
// the wiring level, on top of the pure-allocation proof in the portblock pkg.
func TestSinglePlanConcurrentSlicesGetDisjointBlocks(t *testing.T) {
	run := func(order int) map[string]string {
		p := prd.PRD{ID: "feat", Title: "F", UserStories: []prd.UserStory{{ID: "US-001", Title: "S1", Priority: 1}}}
		root, project := projectFixtureWithOrder(t, "feat", order, p)
		runner := &envCapturingRunner{replies: []coreruntime.Result{makePassAndCompleteResult("US-001")}}
		res := planrun.SinglePlan(planrun.SinglePlanInput{
			Project:      project,
			ControlRoot:  root,
			WorktreeBase: ".worktrees",
			AgentIDs:     []agents.ID{agents.AgentClaude},
			Runner:       runner,
			Manager:      &planrun.Manager{Git: newFakeGit()},
			ProjectRoot:  root,
		})
		if res.Err != nil {
			t.Fatalf("SinglePlan(order=%d): %v", order, res.Err)
		}
		return runner.capturedEnv[0]
	}
	if a, b := run(1)[portblock.EnvPort], run(2)[portblock.EnvPort]; a == b {
		t.Fatalf("ordinals 1 and 2 shared first port %q — not disjoint", a)
	}
}
