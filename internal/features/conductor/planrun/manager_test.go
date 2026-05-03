package planrun_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planrun"
)

// fakeGit is a scripted Git double for preflight tests.
type fakeGit struct {
	repo            bool
	dirty           bool
	currentBranch   string
	resolveOK       map[string]string
	branches        map[string]struct{}
	worktreePaths   []string
	createNew       []string
	createExisting  []string
	currentBranchOK bool
}

func newFakeGit() *fakeGit {
	return &fakeGit{
		repo:            true,
		currentBranch:   "main",
		currentBranchOK: true,
		resolveOK:       map[string]string{"main": "deadbeefcafef00d"},
		branches:        map[string]struct{}{},
	}
}

func (g *fakeGit) IsRepo(string) (bool, error) { return g.repo, nil }
func (g *fakeGit) IsDirty(string) (bool, error) { return g.dirty, nil }
func (g *fakeGit) ResolveRef(_, ref string) (string, error) {
	sha, ok := g.resolveOK[ref]
	if !ok {
		return "", fmt.Errorf("unknown ref %q", ref)
	}
	return sha, nil
}
func (g *fakeGit) CurrentBranch(string) (string, error) {
	if !g.currentBranchOK {
		return "", errors.New("detached")
	}
	return g.currentBranch, nil
}
func (g *fakeGit) BranchExists(_, branch string) (bool, error) {
	_, ok := g.branches[branch]
	return ok, nil
}
func (g *fakeGit) WorktreeListPaths(string) ([]string, error) { return g.worktreePaths, nil }
func (g *fakeGit) WorktreeAddNewBranch(_, path, branch, base string) error {
	g.createNew = append(g.createNew, path+"|"+branch+"|"+base)
	g.branches[branch] = struct{}{}
	return os.MkdirAll(path, 0o755)
}
func (g *fakeGit) WorktreeAddExistingBranch(_, path, branch string) error {
	g.createExisting = append(g.createExisting, path+"|"+branch)
	return os.MkdirAll(path, 0o755)
}

func TestPrepareCleanFirstRunPlansFreshWorktree(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "springfield/plans/p.md"), "plan")

	g := newFakeGit()
	m := &planrun.Manager{Git: g}
	dec, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "feature-a", Path: "springfield/plans/p.md", Order: 1},
		AllStates:    map[string]*conductor.PlanState{},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if dec.Reuse {
		t.Fatalf("first run should not reuse")
	}
	if dec.Reason != "clean-first-run" {
		t.Fatalf("Reason: %q", dec.Reason)
	}
	if dec.Context.WorktreeRoot != filepath.Join(root, ".worktrees", "feature-a") {
		t.Fatalf("WorktreeRoot: %q", dec.Context.WorktreeRoot)
	}
	if dec.Context.Branch != "springfield/feature-a" {
		t.Fatalf("Branch: %q", dec.Context.Branch)
	}
	if dec.Context.BaseRef != "main" || dec.Context.BaseHead != "deadbeefcafef00d" {
		t.Fatalf("base mismatch: %+v", dec.Context)
	}
}

func TestPrepareDirtySourceRefusesFirstRun(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "springfield/plans/p.md"), "plan")

	g := newFakeGit()
	g.dirty = true
	m := &planrun.Manager{Git: g}
	_, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "p", Path: "springfield/plans/p.md", Order: 1},
		AllStates:    map[string]*conductor.PlanState{},
	})
	if err == nil {
		t.Fatalf("expected dirty rejection")
	}
	pe := planrun.AsPreflight(err)
	if pe == nil || pe.Tag != "preflight-dirty-source" {
		t.Fatalf("expected preflight-dirty-source, got %v", err)
	}
}

func TestPrepareResumeReusesWhenDigestStable(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "springfield/plans/p.md"), "plan body v1")

	unit := conductor.PlanUnit{ID: "p", Path: "springfield/plans/p.md", Order: 1}
	wt := filepath.Join(root, ".worktrees", "p")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir wt: %v", err)
	}

	digest, err := planrun.InputDigest(root, unit)
	if err != nil {
		t.Fatalf("InputDigest: %v", err)
	}

	prior := &conductor.PlanState{
		Status:       conductor.StatusFailed,
		WorktreePath: wt,
		Branch:       "springfield/p",
		BaseRef:      "main",
		BaseHead:     "deadbeefcafef00d",
		InputDigest:  digest,
	}

	g := newFakeGit()
	// Even with dirty source, resume must still work because the dirt is
	// in the worktree, not the control checkout.
	g.dirty = true
	m := &planrun.Manager{Git: g}
	dec, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         unit,
		PriorState:   prior,
		AllStates:    map[string]*conductor.PlanState{"p": prior},
	})
	if err != nil {
		t.Fatalf("Prepare resume: %v", err)
	}
	if !dec.Reuse {
		t.Fatalf("expected reuse")
	}
	if dec.Context.WorktreeRoot != wt {
		t.Fatalf("WorktreeRoot drift: %q vs %q", dec.Context.WorktreeRoot, wt)
	}
}

func TestPrepareResumeRefusesOnInputDrift(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "springfield/plans/p.md"), "plan body v1")

	unit := conductor.PlanUnit{ID: "p", Path: "springfield/plans/p.md", Order: 1}
	wt := filepath.Join(root, ".worktrees", "p")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir wt: %v", err)
	}

	prior := &conductor.PlanState{
		Status:       conductor.StatusFailed,
		WorktreePath: wt,
		InputDigest:  "sha256:stale",
	}

	g := newFakeGit()
	m := &planrun.Manager{Git: g}
	_, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         unit,
		PriorState:   prior,
		AllStates:    map[string]*conductor.PlanState{"p": prior},
	})
	if err == nil {
		t.Fatalf("expected drift rejection")
	}
	pe := planrun.AsPreflight(err)
	if pe == nil || pe.Tag != "preflight-input-drift" {
		t.Fatalf("expected preflight-input-drift, got %v", err)
	}
}

func TestPrepareRefusesUntrackedWorktreeOnDisk(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "springfield/plans/p.md"), "plan")
	wt := filepath.Join(root, ".worktrees", "p")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	g := newFakeGit()
	// no entries in WorktreeListPaths → not tracked
	m := &planrun.Manager{Git: g}
	_, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "p", Path: "springfield/plans/p.md", Order: 1},
		AllStates:    map[string]*conductor.PlanState{},
	})
	if err == nil {
		t.Fatalf("expected rejection")
	}
	pe := planrun.AsPreflight(err)
	if pe == nil || !strings.Contains(pe.Tag, "preflight-worktree") {
		t.Fatalf("expected worktree preflight rejection, got %v", err)
	}
}

func TestPrepareRefusesAlreadyCompletedPlan(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "springfield/plans/p.md"), "plan")

	g := newFakeGit()
	m := &planrun.Manager{Git: g}
	_, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "p", Path: "springfield/plans/p.md", Order: 1},
		PriorState:   &conductor.PlanState{Status: conductor.StatusCompleted},
		AllStates:    map[string]*conductor.PlanState{"p": {Status: conductor.StatusCompleted}},
	})
	if err == nil {
		t.Fatalf("expected refusal")
	}
	pe := planrun.AsPreflight(err)
	if pe == nil || pe.Tag != "preflight-already-completed" {
		t.Fatalf("expected preflight-already-completed, got %v", err)
	}
}

func TestPrepareRefusesNonRepo(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "springfield/plans/p.md"), "plan")

	g := newFakeGit()
	g.repo = false
	m := &planrun.Manager{Git: g}
	_, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "p", Path: "springfield/plans/p.md", Order: 1},
		AllStates:    map[string]*conductor.PlanState{},
	})
	if err == nil {
		t.Fatalf("expected non-repo rejection")
	}
	pe := planrun.AsPreflight(err)
	if pe == nil || pe.Tag != "preflight-not-a-repo" {
		t.Fatalf("expected preflight-not-a-repo, got %v", err)
	}
}

func TestCreateWorktreeAddsNewBranchWhenAbsent(t *testing.T) {
	g := newFakeGit()
	m := &planrun.Manager{Git: g}
	root := t.TempDir()
	wt := filepath.Join(root, ".worktrees", "p")
	ctx := planrun.Context{
		ControlRoot:  root,
		WorktreeRoot: wt,
		PlanKey:      "p",
		Branch:       "springfield/p",
		BaseRef:      "main",
	}
	if err := m.CreateWorktree(ctx); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	if len(g.createNew) != 1 {
		t.Fatalf("expected one new-branch worktree add, got %v", g.createNew)
	}
	if !strings.Contains(g.createNew[0], "springfield/p") {
		t.Fatalf("create cmd missing branch: %v", g.createNew)
	}
}

func TestCreateWorktreeAddsExistingBranchWhenPresent(t *testing.T) {
	g := newFakeGit()
	g.branches["springfield/p"] = struct{}{}
	m := &planrun.Manager{Git: g}
	root := t.TempDir()
	wt := filepath.Join(root, ".worktrees", "p")
	ctx := planrun.Context{
		ControlRoot:  root,
		WorktreeRoot: wt,
		PlanKey:      "p",
		Branch:       "springfield/p",
		BaseRef:      "main",
	}
	if err := m.CreateWorktree(ctx); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	if len(g.createNew) != 0 {
		t.Fatalf("must not create new branch when existing")
	}
	if len(g.createExisting) != 1 {
		t.Fatalf("expected existing-branch add, got %v", g.createExisting)
	}
}
