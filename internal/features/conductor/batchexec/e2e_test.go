package batchexec_test

// End-to-end concurrency test: two real SinglePlan + planmerge.Retain flows
// (per-plan-branches mode) driven concurrently through batchexec.Execute
// against ONE shared conductor.Project. A rendezvous barrier inside the fake
// agent proves the plans genuinely overlap; run under -race this verifies the
// whole locked-state design (Project mutex, WithGitLock serialization,
// snapshot-based preflight) with the production wiring, not test doubles of
// it. The fake git is deliberately mutex-free: if production code ever calls
// a mutating git op outside WithGitLock, -race fails this test.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/batchexec"
	"springfield/internal/features/conductor/planmerge"
	"springfield/internal/features/conductor/planrun"
)

// e2eGit implements planrun.Git with mutable, UNLOCKED maps — WithGitLock in
// production code is the only thing keeping concurrent access race-free.
type e2eGit struct {
	branches map[string]struct{}
	resolve  map[string]string
}

func newE2EGit() *e2eGit {
	return &e2eGit{
		branches: map[string]struct{}{},
		resolve:  map[string]string{"main": "deadbeefcafef00d"},
	}
}

func (g *e2eGit) IsRepo(string) (bool, error)          { return true, nil }
func (g *e2eGit) IsDirty(string) (bool, error)         { return false, nil }
func (g *e2eGit) CurrentBranch(string) (string, error) { return "main", nil }
func (g *e2eGit) ResolveRef(_, ref string) (string, error) {
	sha, ok := g.resolve[ref]
	if !ok {
		return "", fmt.Errorf("unknown ref %q", ref)
	}
	return sha, nil
}
func (g *e2eGit) BranchExists(_, branch string) (bool, error) {
	_, ok := g.branches[branch]
	return ok, nil
}
func (g *e2eGit) WorktreeListPaths(string) ([]string, error) { return nil, nil }
func (g *e2eGit) WorktreeAddNewBranch(_, path, branch, _ string) error {
	g.branches[branch] = struct{}{}
	return os.MkdirAll(path, 0o755)
}
func (g *e2eGit) WorktreeAddExistingBranch(_, path, _ string) error {
	return os.MkdirAll(path, 0o755)
}
func (g *e2eGit) Head(string) (string, error)      { return "headcafef00d", nil }
func (g *e2eGit) Diff(_, _ string) (string, error) { return "", nil }

// e2eMergeGit implements the planmerge.Git subset Retain exercises; the
// worktree-removal map is unlocked for the same reason as e2eGit.
type e2eMergeGit struct {
	removed map[string]bool
}

func (g *e2eMergeGit) ResolveRef(_, _ string) (string, error)   { return "deadbeefcafef00d", nil }
func (g *e2eMergeGit) Head(string) (string, error)              { return "headcafef00d", nil }
func (g *e2eMergeGit) CurrentBranch(string) (string, error)     { return "main", nil }
func (g *e2eMergeGit) IsDirty(string) (bool, error)             { return false, nil }
func (g *e2eMergeGit) IsDirtyAgainst(_, _ string) (bool, error) { return false, nil }
func (g *e2eMergeGit) WorktreeAddDetached(_, _, _ string) error { return errors.New("unused") }
func (g *e2eMergeGit) MergeFFOnly(_, _ string) error            { return errors.New("unused") }
func (g *e2eMergeGit) UpdateBranchRef(_, _, _, _ string) error  { return errors.New("unused") }
func (g *e2eMergeGit) WorktreeRemoveForce(_, path string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	g.removed[path] = true
	return nil
}
func (g *e2eMergeGit) BranchDelete(_, _ string) error { return errors.New("unused") }
func (g *e2eMergeGit) ResetHard(_, _ string) error    { return errors.New("unused") }

// rendezvousAgent blocks every plan's agent call until all expected plans
// have arrived — deterministic proof the plans run concurrently, not merely
// interleaved.
type rendezvousAgent struct {
	arrive *sync.WaitGroup
}

func (a *rendezvousAgent) Run(_ context.Context, req coreruntime.Request) coreruntime.Result {
	a.arrive.Done()
	a.arrive.Wait() // deadlocks (test timeout) unless siblings truly overlap
	return coreruntime.Result{
		Agent:    agents.AgentClaude,
		Status:   coreruntime.StatusPassed,
		ExitCode: 0,
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>"},
		},
		StartedAt: time.Now().Add(-time.Second),
		EndedAt:   time.Now(),
	}
}

// e2eRunner mirrors cmd's batchPlanRunner shape: SinglePlan + Retain per
// plan, IsTerminal via the locked ReadPlan.
type e2eRunner struct {
	project  *conductor.Project
	root     string
	git      *e2eGit
	mergeGit *e2eMergeGit
	agent    *rendezvousAgent
}

func (r *e2eRunner) IsTerminal(planID string) bool {
	st, ok := r.project.ReadPlan(planID)
	return ok && st.IsIntegrated()
}

func (r *e2eRunner) RunPlan(ctx context.Context, planID string, _ batchexec.RunInfo) batchexec.Outcome {
	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      r.project,
		ControlRoot:  r.root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       r.agent,
		Manager:      &planrun.Manager{Git: r.git},
		TargetPlanID: planID,
		Ctx:          ctx,
		BatchID:      "e2e-batch",
	})
	if res.Err != nil {
		return batchexec.Outcome{Err: res.Err}
	}
	merge := planmerge.Retain(planmerge.RetainInput{
		Project:     r.project,
		PlanID:      planID,
		ControlRoot: r.root,
		Git:         r.mergeGit,
	})
	if merge.Err != nil {
		return batchexec.Outcome{Err: merge.Err}
	}
	return batchexec.Outcome{}
}

func writeE2EPlan(t *testing.T, root, planID string) {
	t.Helper()
	dir := filepath.Join(root, ".springfield", "plans", planID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	prd := map[string]any{
		"id": planID, "title": planID,
		"user_stories": []map[string]any{
			{"id": "US-001", "title": "S", "passes": false, "priority": 1, "deps": []string{}, "acceptance_criteria": []string{}},
		},
	}
	data, _ := json.MarshalIndent(prd, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "prd.json"), data, 0o644); err != nil {
		t.Fatalf("write prd: %v", err)
	}
}

func TestConcurrentPerPlanBatchEndToEnd(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "springfield.toml"),
		[]byte("[project]\nagent_priority = [\"claude\"]\n"), 0o644); err != nil {
		t.Fatalf("toml: %v", err)
	}
	writeE2EPlan(t, root, "alpha")
	writeE2EPlan(t, root, "beta")
	cfg := map[string]any{
		"plans_dir": ".springfield/plans", "worktree_base": ".worktrees",
		"max_retries": 1, "tool": "claude",
		"plan_units": []map[string]any{
			{"id": "alpha", "path": ".springfield/plans/alpha/prd.json", "order": 1},
			{"id": "beta", "path": ".springfield/plans/beta/prd.json", "order": 2},
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

	var arrive sync.WaitGroup
	arrive.Add(2)
	runner := &e2eRunner{
		project:  project,
		root:     root,
		git:      newE2EGit(),
		mergeGit: &e2eMergeGit{removed: map[string]bool{}},
		agent:    &rendezvousAgent{arrive: &arrive},
	}

	res, err := batchexec.Execute(context.Background(), batchexec.Input{
		Batch: batch.Batch{
			ID:      "e2e-batch",
			Phases:  []batch.Phase{{Mode: batch.PhaseParallel, Plans: []string{"alpha", "beta"}}},
			PlanIDs: []string{"alpha", "beta"},
		},
		Runner:      runner,
		Parallelize: true,
		MaxParallel: 2,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.CostCapped || res.Cancelled {
		t.Fatalf("unexpected result: %+v", res)
	}

	for _, planID := range []string{"alpha", "beta"} {
		st, ok := project.ReadPlan(planID)
		if !ok || !st.IsIntegrated() {
			t.Errorf("plan %s not integrated: %+v", planID, st)
		}
		if st.Branch == "" {
			t.Errorf("plan %s: branch not recorded", planID)
			continue
		}
		if _, exists := runner.git.branches[st.Branch]; !exists {
			t.Errorf("plan %s: standalone branch %s was not created/retained", planID, st.Branch)
		}
	}
}
