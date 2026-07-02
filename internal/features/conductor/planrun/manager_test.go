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
	branchByPath    map[string]string
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

func (g *fakeGit) IsRepo(string) (bool, error)  { return g.repo, nil }
func (g *fakeGit) IsDirty(string) (bool, error) { return g.dirty, nil }
func (g *fakeGit) ResolveRef(_, ref string) (string, error) {
	sha, ok := g.resolveOK[ref]
	if !ok {
		return "", fmt.Errorf("unknown ref %q", ref)
	}
	return sha, nil
}
func (g *fakeGit) CurrentBranch(dir string) (string, error) {
	if !g.currentBranchOK {
		return "", errors.New("detached")
	}
	if branch, ok := g.branchByPath[dir]; ok {
		return branch, nil
	}
	return g.currentBranch, nil
}
func (g *fakeGit) BranchExists(_, branch string) (bool, error) {
	_, ok := g.branches[branch]
	return ok, nil
}
func (g *fakeGit) WorktreeListPaths(string) ([]string, error) { return g.worktreePaths, nil }
func (g *fakeGit) Head(string) (string, error)                { return "headcafef00d", nil }
func (g *fakeGit) Diff(_, _ string) (string, error)           { return "fake-diff", nil }
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
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit()
	m := &planrun.Manager{Git: g}
	dec, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "feature-a", Path: ".springfield/plans/p.md", Order: 1},
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

// dirtyOnlySpringfieldFakeGit reports porcelain output that contains only
// Springfield-owned paths so the IsDirty filter has something to filter.
// The CLIGit filter logic is exercised separately; this fake just proves
// Manager.Prepare succeeds when Git.IsDirty returns false.
func TestPrepareTreatsSpringfieldOwnedPathsAsClean(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit()
	g.dirty = false // canonical: filter has already collapsed Springfield-only dirt to clean
	m := &planrun.Manager{Git: g}
	dec, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1},
		AllStates:    map[string]*conductor.PlanState{},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if dec.Reason != "clean-first-run" {
		t.Fatalf("Reason: %q", dec.Reason)
	}
}

func TestPrepareDirtySourceRefusesFirstRun(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit()
	g.dirty = true
	m := &planrun.Manager{Git: g}
	_, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1},
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
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan body v1")

	unit := conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1}
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
	g.worktreePaths = []string{wt}
	g.branchByPath = map[string]string{wt: "springfield/p"}
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

func TestPrepareResumeRefusesUntrackedWorktreePath(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan body")

	unit := conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1}
	wt := filepath.Join(root, ".worktrees", "p")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir wt: %v", err)
	}
	digest, err := planrun.InputDigest(root, unit)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	prior := &conductor.PlanState{
		Status:       conductor.StatusFailed,
		WorktreePath: wt,
		Branch:       "springfield/p",
		InputDigest:  digest,
	}

	g := newFakeGit()
	// path exists on disk but is NOT in `git worktree list` — was deleted
	// and recreated outside Springfield.
	m := &planrun.Manager{Git: g}
	_, err = m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         unit,
		PriorState:   prior,
		AllStates:    map[string]*conductor.PlanState{"p": prior},
	})
	if err == nil {
		t.Fatalf("expected refusal for untracked recorded path")
	}
	pe := planrun.AsPreflight(err)
	if pe == nil || pe.Tag != "preflight-worktree-untracked" {
		t.Fatalf("expected preflight-worktree-untracked, got %v", err)
	}
}

func TestPrepareResumeRefusesBranchMismatch(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan body")

	unit := conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1}
	wt := filepath.Join(root, ".worktrees", "p")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir wt: %v", err)
	}
	digest, err := planrun.InputDigest(root, unit)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	prior := &conductor.PlanState{
		Status:       conductor.StatusFailed,
		WorktreePath: wt,
		Branch:       "springfield/p",
		InputDigest:  digest,
	}

	g := newFakeGit()
	g.worktreePaths = []string{wt}
	// branch on the recorded path is now something else (user checked out
	// a different branch in the worktree).
	g.branchByPath = map[string]string{wt: "feature/other"}
	m := &planrun.Manager{Git: g}
	_, err = m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         unit,
		PriorState:   prior,
		AllStates:    map[string]*conductor.PlanState{"p": prior},
	})
	if err == nil {
		t.Fatalf("expected branch-mismatch refusal")
	}
	pe := planrun.AsPreflight(err)
	if pe == nil || pe.Tag != "preflight-worktree-branch-mismatch" {
		t.Fatalf("expected preflight-worktree-branch-mismatch, got %v", err)
	}
}

func TestPrepareResumeRefusesOnInputDrift(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan body v1")

	unit := conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1}
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
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")
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
		Unit:         conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1},
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
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit()
	m := &planrun.Manager{Git: g}
	_, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1},
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

// TestPrepareRefusesUnknownLocalBranchRef verifies the slice-3 preflight
// contract: an explicit Unit.Ref that does not name a local branch is
// rejected up front so the merge phase never sees an unpublishable target.
func TestPrepareRefusesUnknownLocalBranchRef(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit()
	// Branch "feature/missing" is NOT in g.branches → BranchExists returns false.
	m := &planrun.Manager{Git: g}
	_, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Ref: "feature/missing", Order: 1},
		AllStates:    map[string]*conductor.PlanState{},
	})
	if err == nil {
		t.Fatalf("expected non-local-branch rejection")
	}
	pe := planrun.AsPreflight(err)
	if pe == nil || pe.Tag != "preflight-ref-not-local-branch" {
		t.Fatalf("expected preflight-ref-not-local-branch, got %v", err)
	}
	if len(g.createNew)+len(g.createExisting) != 0 {
		t.Fatalf("worktree side effects must not fire on ref preflight failure")
	}
}

func TestPrepareAcceptsExplicitLocalBranchRef(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit()
	g.branches["release"] = struct{}{}
	g.resolveOK["release"] = "cafef00d"
	m := &planrun.Manager{Git: g}
	dec, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Ref: "release", Order: 1},
		AllStates:    map[string]*conductor.PlanState{},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if dec.Context.BaseRef != "release" || dec.Context.BaseHead != "cafef00d" {
		t.Fatalf("unexpected context: %+v", dec.Context)
	}
}

func TestPrepareRefusesProtectedBaseByDefault(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit() // currentBranch defaults to "main"
	m := &planrun.Manager{Git: g}
	_, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:          root,
		WorktreeBase:         ".worktrees",
		Unit:                 conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1},
		AllStates:            map[string]*conductor.PlanState{},
		EnforceProtectedBase: true,
	})
	if err == nil {
		t.Fatal("expected protected-base rejection")
	}
	pe := planrun.AsPreflight(err)
	if pe == nil || pe.Tag != "preflight-protected-base" {
		t.Fatalf("expected preflight-protected-base, got %v", err)
	}
	if !strings.Contains(pe.Message, "allow_protected_base") {
		t.Fatalf("error must point at the opt-out, got: %q", pe.Message)
	}
	if len(g.createNew)+len(g.createExisting) != 0 {
		t.Fatalf("worktree side effects must not fire on protected-base rejection")
	}
}

func TestPrepareRefusesProtectedBaseFromExplicitRef(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit()
	g.branches["master"] = struct{}{}
	g.resolveOK["master"] = "cafef00d"
	m := &planrun.Manager{Git: g}
	_, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:          root,
		WorktreeBase:         ".worktrees",
		Unit:                 conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Ref: "master", Order: 1},
		AllStates:            map[string]*conductor.PlanState{},
		EnforceProtectedBase: true,
	})
	if err == nil {
		t.Fatal("expected protected-base rejection on explicit master")
	}
	pe := planrun.AsPreflight(err)
	if pe == nil || pe.Tag != "preflight-protected-base" {
		t.Fatalf("expected preflight-protected-base, got %v", err)
	}
}

func TestPrepareAllowsProtectedBaseWhenGuardOff(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit() // currentBranch=main
	m := &planrun.Manager{Git: g}
	dec, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:          root,
		WorktreeBase:         ".worktrees",
		Unit:                 conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1},
		AllStates:            map[string]*conductor.PlanState{},
		EnforceProtectedBase: false,
	})
	if err != nil {
		t.Fatalf("Prepare with guard off should succeed: %v", err)
	}
	if dec.Context.BaseRef != "main" {
		t.Fatalf("BaseRef: %q", dec.Context.BaseRef)
	}
}

func TestPrepareGuardUsesPriorStateBaseRefOnResume(t *testing.T) {
	// Resume case: plan was originally anchored to a feature branch and
	// has a reusable worktree on disk. The operator's current checkout
	// happens to be a protected branch (e.g. they switched to main to
	// pull). The guard must consult PriorState.BaseRef rather than the
	// caller's current branch so the resume isn't falsely refused.
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	wtPath := filepath.Join(root, ".worktrees", "p")
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}

	g := newFakeGit() // currentBranch = "main"
	g.branches["springfield/p"] = struct{}{}
	g.worktreePaths = []string{wtPath}
	g.branchByPath = map[string]string{wtPath: "springfield/p"}
	g.resolveOK["feat/redesign"] = "feedface"

	priorDigest, err := planrun.InputDigest(root, conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md"})
	if err != nil {
		t.Fatalf("digest: %v", err)
	}

	m := &planrun.Manager{Git: g}
	dec, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1},
		PriorState: &conductor.PlanState{
			Status:       conductor.StatusFailed,
			WorktreePath: wtPath,
			Branch:       "springfield/p",
			BaseRef:      "feat/redesign",
			BaseHead:     "feedface",
			InputDigest:  priorDigest,
		},
		AllStates:            map[string]*conductor.PlanState{},
		EnforceProtectedBase: true,
	})
	if err != nil {
		t.Fatalf("resume on feature branch must pass guard even when current checkout is main; got: %v", err)
	}
	if !dec.Reuse {
		t.Fatalf("expected reuse on resume, got fresh: %+v", dec)
	}
	if dec.Context.BaseRef != "feat/redesign" {
		t.Fatalf("Context.BaseRef should reflect prior anchor, got %q", dec.Context.BaseRef)
	}
}

func TestPrepareGuardRefusesWhenPriorBaseIsProtected(t *testing.T) {
	// Symmetric to the resume case: if PriorState.BaseRef itself is
	// protected, the guard must still fire even when the operator's
	// current checkout is a feature branch (e.g. config flipped from
	// allow_protected_base = true to false between runs).
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit()
	g.currentBranch = "feat/redesign"
	g.resolveOK["feat/redesign"] = "feedface"

	m := &planrun.Manager{Git: g}
	_, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1},
		PriorState: &conductor.PlanState{
			Status:  conductor.StatusFailed,
			BaseRef: "main",
		},
		AllStates:            map[string]*conductor.PlanState{},
		EnforceProtectedBase: true,
	})
	if err == nil {
		t.Fatal("expected guard to refuse when PriorState.BaseRef is protected")
	}
	pe := planrun.AsPreflight(err)
	if pe == nil || pe.Tag != "preflight-protected-base" {
		t.Fatalf("expected preflight-protected-base, got %v", err)
	}
	if !strings.Contains(pe.Message, "main") {
		t.Fatalf("error must mention the recorded base, got: %q", pe.Message)
	}
}

func TestPrepareAllowsNonProtectedBaseUnderGuard(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit()
	g.currentBranch = "feat/redesign"
	g.resolveOK["feat/redesign"] = "feedface"
	m := &planrun.Manager{Git: g}
	dec, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:          root,
		WorktreeBase:         ".worktrees",
		Unit:                 conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1},
		AllStates:            map[string]*conductor.PlanState{},
		EnforceProtectedBase: true,
	})
	if err != nil {
		t.Fatalf("feat/redesign must pass guard: %v", err)
	}
	if dec.Context.BaseRef != "feat/redesign" {
		t.Fatalf("BaseRef: %q", dec.Context.BaseRef)
	}
}

func TestPrepareRefusesNonRepo(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit()
	g.repo = false
	m := &planrun.Manager{Git: g}
	_, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1},
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

func TestPrepareRejectsMissingPlanFileBeforeWorktreeSideEffects(t *testing.T) {
	root := t.TempDir()
	// no plan body on disk

	g := newFakeGit()
	m := &planrun.Manager{Git: g}
	_, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "p", Path: ".springfield/plans/missing.md", Order: 1},
		AllStates:    map[string]*conductor.PlanState{},
	})
	if err == nil {
		t.Fatalf("expected plan-missing rejection")
	}
	pe := planrun.AsPreflight(err)
	if pe == nil || pe.Tag != "preflight-plan-missing" {
		t.Fatalf("expected preflight-plan-missing, got %v", err)
	}
	if len(g.createNew)+len(g.createExisting) != 0 {
		t.Fatalf("worktree side effects fired: new=%v existing=%v", g.createNew, g.createExisting)
	}
}

// fakeDisk is a scripted DiskChecker double for the disk-space preflight.
type fakeDisk struct {
	avail uint64
	err   error
	asked []string
}

func (d *fakeDisk) AvailableBytes(path string) (uint64, error) {
	d.asked = append(d.asked, path)
	return d.avail, d.err
}

func TestPrepareRefusesInsufficientDisk(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit()
	d := &fakeDisk{avail: 100 << 20} // 100 MiB free
	m := &planrun.Manager{Git: g, Disk: d}
	_, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:      root,
		WorktreeBase:     ".worktrees",
		Unit:             conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1},
		AllStates:        map[string]*conductor.PlanState{},
		MinFreeDiskBytes: 2 << 30, // require 2 GiB
	})
	if err == nil {
		t.Fatalf("expected insufficient-disk rejection")
	}
	pe := planrun.AsPreflight(err)
	if pe == nil || pe.Tag != "preflight-insufficient-disk" {
		t.Fatalf("expected preflight-insufficient-disk, got %v", err)
	}
	if len(g.createNew)+len(g.createExisting) != 0 {
		t.Fatalf("worktree side effects must not fire on insufficient-disk rejection")
	}
}

func TestPrepareAllowsSufficientDisk(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit()
	d := &fakeDisk{avail: 50 << 30} // 50 GiB free
	m := &planrun.Manager{Git: g, Disk: d}
	dec, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:      root,
		WorktreeBase:     ".worktrees",
		Unit:             conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1},
		AllStates:        map[string]*conductor.PlanState{},
		MinFreeDiskBytes: 2 << 30,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if dec.Reason != "clean-first-run" {
		t.Fatalf("Reason: %q", dec.Reason)
	}
	if len(d.asked) == 0 {
		t.Fatalf("disk checker should have been consulted")
	}
}

// MinFreeDiskBytes == 0 must select the built-in default floor (1 GiB), so a
// caller that doesn't configure a floor still gets ENOSPC protection.
func TestPrepareAppliesDefaultDiskFloorWhenUnset(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit()
	d := &fakeDisk{avail: 500 << 20} // 500 MiB — below the 1 GiB default floor
	m := &planrun.Manager{Git: g, Disk: d}
	_, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1},
		AllStates:    map[string]*conductor.PlanState{},
		// MinFreeDiskBytes deliberately omitted (zero) → default floor applies.
	})
	if err == nil {
		t.Fatalf("expected insufficient-disk rejection from the default floor")
	}
	pe := planrun.AsPreflight(err)
	if pe == nil || pe.Tag != "preflight-insufficient-disk" {
		t.Fatalf("expected preflight-insufficient-disk, got %v", err)
	}
}

// A reuse/resume run does not re-checkout or re-install, so the disk
// preflight must not block it even when free space is below the floor.
func TestPrepareResumeSkipsDiskPreflight(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan body v1")

	unit := conductor.PlanUnit{ID: "p", Path: ".springfield/plans/p.md", Order: 1}
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
	g.worktreePaths = []string{wt}
	g.branchByPath = map[string]string{wt: "springfield/p"}
	d := &fakeDisk{avail: 1 << 20} // 1 MiB — far below any floor
	m := &planrun.Manager{Git: g, Disk: d}
	dec, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:      root,
		WorktreeBase:     ".worktrees",
		Unit:             unit,
		PriorState:       prior,
		AllStates:        map[string]*conductor.PlanState{"p": prior},
		MinFreeDiskBytes: 2 << 30,
	})
	if err != nil {
		t.Fatalf("resume must not be blocked by disk preflight: %v", err)
	}
	if !dec.Reuse {
		t.Fatalf("expected reuse")
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

func TestPrepareUsesBatchBaseRefWhenUnitRefEmpty(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit() // currentBranch = "main"
	g.branches["develop"] = struct{}{}
	g.resolveOK["develop"] = "d0d0d0d0d0d0"
	m := &planrun.Manager{Git: g}
	dec, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "feature-a", Path: ".springfield/plans/p.md", Order: 1},
		AllStates:    map[string]*conductor.PlanState{},
		BatchBaseRef: "develop",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if dec.Context.BaseRef != "develop" || dec.Context.BaseHead != "d0d0d0d0d0d0" {
		t.Fatalf("BatchBaseRef must replace current-branch fallback: %+v", dec.Context)
	}
}

func TestPrepareUnitRefBeatsBatchBaseRef(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".springfield/plans/p.md"), "plan")

	g := newFakeGit()
	g.branches["feat/stack"] = struct{}{}
	g.resolveOK["feat/stack"] = "5tack5tack"
	g.branches["develop"] = struct{}{}
	g.resolveOK["develop"] = "d0d0d0d0d0d0"
	m := &planrun.Manager{Git: g}
	dec, err := m.Prepare(planrun.PrepareInput{
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Unit:         conductor.PlanUnit{ID: "feature-a", Path: ".springfield/plans/p.md", Ref: "feat/stack", Order: 1},
		AllStates:    map[string]*conductor.PlanState{},
		BatchBaseRef: "develop",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if dec.Context.BaseRef != "feat/stack" {
		t.Fatalf("Unit.Ref must beat BatchBaseRef, got %q", dec.Context.BaseRef)
	}
}
