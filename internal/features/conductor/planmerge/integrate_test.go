package planmerge_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planmerge"
)

// fakeGit is a scripted Git double for planmerge tests. Each method records
// the calls it sees and consults a configured response so every divergence
// path can be exercised without a real git binary.
type fakeGit struct {
	headByDir         map[string]string
	resolveByRef      map[string]string
	worktreeAddErr    error
	worktreeAddCalls  []string
	mergeErr          error
	mergeCalls        []string
	updateRefErr      error
	updateRefCalls    []string
	worktreeRemoveErr map[string]error
	worktreeRemoveAll []string
	branchDeleteErr   error
	branchDeleteCalls []string
	// currentBranchByDir maps a directory to the branch CurrentBranch
	// returns. An empty entry means CurrentBranch returns an error
	// (simulating detached HEAD or unreadable branch).
	currentBranchByDir map[string]string
	resetHardErr       error
	resetHardCalls     []string
	dirtyByDir         map[string]bool
	isDirtyErr         error
}

func newFakeGit() *fakeGit {
	return &fakeGit{
		headByDir:         map[string]string{},
		resolveByRef:      map[string]string{},
		worktreeRemoveErr: map[string]error{},
	}
}

func (g *fakeGit) ResolveRef(_, ref string) (string, error) {
	sha, ok := g.resolveByRef[ref]
	if !ok {
		return "", errors.New("unknown ref " + ref)
	}
	return sha, nil
}

func (g *fakeGit) Head(dir string) (string, error) {
	sha, ok := g.headByDir[dir]
	if !ok {
		return "", errors.New("unknown HEAD for " + dir)
	}
	return sha, nil
}

func (g *fakeGit) WorktreeAddDetached(_, path, ref string) error {
	g.worktreeAddCalls = append(g.worktreeAddCalls, path+"|"+ref)
	if g.worktreeAddErr != nil {
		return g.worktreeAddErr
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	// Newly created merge worktree starts at the ref's SHA.
	if sha, ok := g.resolveByRef[ref]; ok {
		g.headByDir[path] = sha
	}
	return nil
}

func (g *fakeGit) WorktreeRemoveForce(_, path string) error {
	g.worktreeRemoveAll = append(g.worktreeRemoveAll, path)
	if err, ok := g.worktreeRemoveErr[path]; ok {
		return err
	}
	return nil
}

func (g *fakeGit) MergeFFOnly(dir, branch string) error {
	g.mergeCalls = append(g.mergeCalls, dir+"|"+branch)
	if g.mergeErr != nil {
		return g.mergeErr
	}
	// Successful merge: dir's HEAD advances to the branch's HEAD.
	if sha, ok := g.headByDir["branch:"+branch]; ok {
		g.headByDir[dir] = sha
	}
	return nil
}

func (g *fakeGit) UpdateBranchRef(_, branch, newSHA, expected string) error {
	g.updateRefCalls = append(g.updateRefCalls, branch+"|"+newSHA+"|"+expected)
	if g.updateRefErr != nil {
		return g.updateRefErr
	}
	g.resolveByRef[branch] = newSHA
	return nil
}

func (g *fakeGit) BranchDelete(_, branch string) error {
	g.branchDeleteCalls = append(g.branchDeleteCalls, branch)
	return g.branchDeleteErr
}

func (g *fakeGit) CurrentBranch(dir string) (string, error) {
	if g.currentBranchByDir == nil {
		return "", errors.New("no current branch configured")
	}
	branch, ok := g.currentBranchByDir[dir]
	if !ok || branch == "" {
		return "", errors.New("no current branch for " + dir)
	}
	return branch, nil
}

func (g *fakeGit) ResetHard(_, sha string) error {
	g.resetHardCalls = append(g.resetHardCalls, sha)
	return g.resetHardErr
}

func (g *fakeGit) IsDirty(dir string) (bool, error) {
	if g.isDirtyErr != nil {
		return false, g.isDirtyErr
	}
	if g.dirtyByDir == nil {
		return false, nil
	}
	return g.dirtyByDir[dir], nil
}

// projectFixture writes a minimal Springfield project with one plan unit
// already in StatusCompleted so Integrate has a real on-disk state file to
// update.
func projectFixture(t *testing.T, planID, branch, baseRef, baseHead, planHead string) (string, *conductor.Project, string) {
	t.Helper()
	root := t.TempDir()
	wt := filepath.Join(root, ".worktrees", planID)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir wt: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "springfield.toml"),
		[]byte("[project]\nagent_priority = [\"claude\"]\n"), 0o644); err != nil {
		t.Fatalf("toml: %v", err)
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
	if err := os.MkdirAll(filepath.Join(root, "springfield", "plans"), 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "springfield", "plans", planID+".md"), []byte("# plan\n"), 0o644); err != nil {
		t.Fatalf("plan body: %v", err)
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	project.State.Plans[planID] = &conductor.PlanState{
		Status:       conductor.StatusCompleted,
		Branch:       branch,
		BaseRef:      baseRef,
		BaseHead:     baseHead,
		WorktreePath: wt,
		ExitReason:   "completed",
		PlanHead:     planHead,
	}
	if err := project.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	return root, project, wt
}

func TestIntegrateRefusesMergeOnTargetDrift(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAAAAAAAAAA", "BBBBBBBBBBBB")
	g := newFakeGit()
	g.headByDir[wt] = "BBBBBBBBBBBB"
	// Target main has moved away from recorded base_head.
	g.resolveByRef["main"] = "CCCCCCCCCCCC"

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project:      project,
		PlanID:       "alpha",
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		Git:          g,
	})

	if !planmerge.IsRefused(res) {
		t.Fatalf("expected refused outcome, got %+v", res.Merge)
	}
	if res.Reason != planmerge.ReasonTargetDrift {
		t.Fatalf("Reason = %q, want %q", res.Reason, planmerge.ReasonTargetDrift)
	}
	if res.Merge.TargetHead != "CCCCCCCCCCCC" {
		t.Fatalf("TargetHead = %q, want CCCCCCCCCCCC", res.Merge.TargetHead)
	}
	if len(g.worktreeAddCalls) != 0 {
		t.Fatalf("merge worktree must not be created on refusal; got %v", g.worktreeAddCalls)
	}
	if len(g.mergeCalls) != 0 || len(g.updateRefCalls) != 0 {
		t.Fatalf("merge ops must not run on refusal")
	}

	// Persisted state reflects refusal + preserved artifacts.
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-load: %v", err)
	}
	st := reloaded.State.Plans["alpha"]
	if st.Status != conductor.StatusCompleted {
		t.Fatalf("execution status was rewritten: %s", st.Status)
	}
	if st.Merge == nil || st.Merge.Status != conductor.MergeRefused {
		t.Fatalf("merge state not persisted as refused: %+v", st.Merge)
	}
	if st.Cleanup == nil ||
		st.Cleanup.ExecutionWorktree == nil || st.Cleanup.ExecutionWorktree.Status != conductor.CleanupPreserved ||
		st.Cleanup.PlanBranch == nil || st.Cleanup.PlanBranch.Status != conductor.CleanupPreserved {
		t.Fatalf("execution worktree + plan branch must be preserved on refusal: %+v", st.Cleanup)
	}
	if st.IsIntegrated() {
		t.Fatalf("refused merge must not count as integrated for queue advancement")
	}
}

func TestIntegrateRecordsExecutionSucceededMergeRefused(t *testing.T) {
	// Same as drift test but explicitly verifies execution state stays
	// "completed" while merge state diverges. Slice-3 contract: queue
	// logic later distinguishes "executed" from "integrated".
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	g.resolveByRef["main"] = "CCCC"

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if res.Err != nil {
		t.Fatalf("Integrate: %v", res.Err)
	}

	st := project.State.Plans["alpha"]
	if st.Status != conductor.StatusCompleted {
		t.Fatalf("execution status changed: %s", st.Status)
	}
	if st.Merge.Status != conductor.MergeRefused {
		t.Fatalf("merge status: %s", st.Merge.Status)
	}
	if st.IsIntegrated() {
		t.Fatalf("IsIntegrated must be false on refusal")
	}
}

func TestIntegrateRecordsMergeSucceededCleanupFailed(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	mergeWtPath := filepath.Join(root, ".worktrees", ".merges", "alpha")
	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	g.headByDir["branch:springfield/alpha"] = "BBBB"
	g.resolveByRef["main"] = "AAAA"
	// Plan branch is ahead of base by one commit.
	g.headByDir[mergeWtPath] = "AAAA" // initial; merge-ff-only will advance
	// Cleanup fails on the merge worktree.
	g.worktreeRemoveErr[mergeWtPath] = errors.New("worktree busy")

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if !planmerge.IsSuccess(res) {
		t.Fatalf("expected merge success, got %+v err=%v", res.Merge, res.Err)
	}
	if res.Cleanup.Status != conductor.CleanupFailed {
		t.Fatalf("cleanup status = %s, want failed", res.Cleanup.Status)
	}
	if res.Cleanup.MergeWorktree == nil || res.Cleanup.MergeWorktree.Status != conductor.CleanupFailed {
		t.Fatalf("merge worktree cleanup must be failed: %+v", res.Cleanup.MergeWorktree)
	}
	// Execution worktree + branch deletions still attempted and may have
	// succeeded — this verifies cleanup failure on one artifact does not
	// short-circuit the others.
	if res.Cleanup.ExecutionWorktree == nil || res.Cleanup.ExecutionWorktree.Status != conductor.CleanupSucceeded {
		t.Fatalf("execution wt cleanup should still be attempted: %+v", res.Cleanup.ExecutionWorktree)
	}
	if res.Cleanup.PlanBranch == nil || res.Cleanup.PlanBranch.Status != conductor.CleanupSucceeded {
		t.Fatalf("plan branch cleanup should still be attempted: %+v", res.Cleanup.PlanBranch)
	}

	// Persisted state retains merge success but not integrated for queue.
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-load: %v", err)
	}
	st := reloaded.State.Plans["alpha"]
	if st.Merge.Status != conductor.MergeSucceeded {
		t.Fatalf("persisted merge: %s", st.Merge.Status)
	}
	if st.IsIntegrated() {
		t.Fatalf("cleanup-failed plan must not be queue-advanced")
	}
}

func TestIntegrateUsesDedicatedMergeWorktreePath(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	g.resolveByRef["main"] = "AAAA"
	g.headByDir["branch:springfield/alpha"] = "BBBB"

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if !planmerge.IsSuccess(res) {
		t.Fatalf("merge: %+v err=%v", res.Merge, res.Err)
	}

	gotMergePath := res.Merge.WorktreePath
	if gotMergePath == "" {
		t.Fatalf("merge worktree path missing")
	}
	if gotMergePath == root {
		t.Fatalf("merge must not run in source checkout")
	}
	if gotMergePath == wt {
		t.Fatalf("merge must not run in execution worktree")
	}
	if !strings.Contains(gotMergePath, ".worktrees") || !strings.Contains(gotMergePath, ".merges") {
		t.Fatalf("merge path should be under worktree base inside .merges subdir: %q", gotMergePath)
	}
	if len(g.worktreeAddCalls) != 1 {
		t.Fatalf("expected exactly one merge worktree add, got %v", g.worktreeAddCalls)
	}
	if !strings.HasPrefix(g.worktreeAddCalls[0], gotMergePath+"|") {
		t.Fatalf("worktree add path drift: %q vs %q", g.worktreeAddCalls[0], gotMergePath)
	}
	if len(g.mergeCalls) != 1 || !strings.HasPrefix(g.mergeCalls[0], gotMergePath+"|") {
		t.Fatalf("merge must run inside merge worktree dir, got %v", g.mergeCalls)
	}
}

func TestIntegratePersistsRefsAndPostMergeHead(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	g.resolveByRef["main"] = "AAAA"
	g.headByDir["branch:springfield/alpha"] = "BBBB"

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if !planmerge.IsSuccess(res) {
		t.Fatalf("merge: %+v err=%v", res.Merge, res.Err)
	}

	if res.Merge.TargetRef != "main" {
		t.Fatalf("TargetRef = %q", res.Merge.TargetRef)
	}
	if res.Merge.TargetHead != "AAAA" {
		t.Fatalf("TargetHead = %q", res.Merge.TargetHead)
	}
	if res.Merge.PostMergeHead != "BBBB" {
		t.Fatalf("PostMergeHead = %q, want BBBB (plan head)", res.Merge.PostMergeHead)
	}
	if res.Merge.Mode != string(planmerge.ModeFFOnly) {
		t.Fatalf("Mode = %q", res.Merge.Mode)
	}

	// update-ref must run with the recorded base_head as expected old.
	if len(g.updateRefCalls) != 1 || g.updateRefCalls[0] != "main|BBBB|AAAA" {
		t.Fatalf("update-ref CAS drift: %v", g.updateRefCalls)
	}

	st := project.State.Plans["alpha"]
	if st.PlanHead != "BBBB" {
		t.Fatalf("PlanHead persisted = %q", st.PlanHead)
	}
}

func TestIntegrateFailsOnFFNotPossiblePreservesArtifacts(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	g.resolveByRef["main"] = "AAAA"
	g.headByDir["branch:springfield/alpha"] = "BBBB"
	g.mergeErr = errors.New("not a fast-forward")

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if res.Merge == nil || res.Merge.Status != conductor.MergeFailed {
		t.Fatalf("expected MergeFailed, got %+v", res.Merge)
	}
	if res.Reason != planmerge.ReasonFFNotPossible {
		t.Fatalf("reason = %q", res.Reason)
	}
	if res.Cleanup.Status != conductor.CleanupSkipped {
		t.Fatalf("cleanup must be skipped on failure: %s", res.Cleanup.Status)
	}
	if res.Cleanup.MergeWorktree == nil || res.Cleanup.MergeWorktree.Status != conductor.CleanupPreserved {
		t.Fatalf("merge worktree must be preserved on failure: %+v", res.Cleanup.MergeWorktree)
	}
	if res.Cleanup.ExecutionWorktree.Status != conductor.CleanupPreserved {
		t.Fatalf("execution worktree must be preserved on failure")
	}
	if res.Cleanup.PlanBranch.Status != conductor.CleanupPreserved {
		t.Fatalf("plan branch must be preserved on failure")
	}
	if len(g.updateRefCalls) != 0 {
		t.Fatalf("update-ref must not run after ff-only failure: %v", g.updateRefCalls)
	}
	if len(g.worktreeRemoveAll) != 0 || len(g.branchDeleteCalls) != 0 {
		t.Fatalf("no cleanup deletions on failure: removes=%v branchDeletes=%v", g.worktreeRemoveAll, g.branchDeleteCalls)
	}
}

func TestIntegrateFailsOnUpdateRefCASLossPreservesArtifacts(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	g.resolveByRef["main"] = "AAAA"
	g.headByDir["branch:springfield/alpha"] = "BBBB"
	g.updateRefErr = errors.New("ref changed concurrently")

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if res.Merge.Status != conductor.MergeFailed || res.Reason != planmerge.ReasonRefUpdate {
		t.Fatalf("expected ref-update-failed, got status=%s reason=%s", res.Merge.Status, res.Reason)
	}
	if res.Cleanup.MergeWorktree.Status != conductor.CleanupPreserved {
		t.Fatalf("merge worktree must remain preserved when CAS lost: %+v", res.Cleanup.MergeWorktree)
	}
}

func TestDiagnoseSurfacesPreservedMergeArtifacts(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	g.resolveByRef["main"] = "ZZZZ" // drift
	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if res.Reason != planmerge.ReasonTargetDrift {
		t.Fatalf("setup: expected drift refusal, got %q", res.Reason)
	}

	d := conductor.Diagnose(project)
	if len(d.MergeIssues) != 1 {
		t.Fatalf("expected one merge issue, got %d: %+v", len(d.MergeIssues), d.MergeIssues)
	}
	issue := d.MergeIssues[0]
	if issue.MergeStatus != conductor.MergeRefused {
		t.Fatalf("issue status = %s", issue.MergeStatus)
	}
	if !strings.Contains(d.NextStep, "merge integration issues") {
		t.Fatalf("NextStep should mention merge issues: %q", d.NextStep)
	}
	report := d.Report()
	if !strings.Contains(report, "Merge issues") {
		t.Fatalf("report missing merge issues section:\n%s", report)
	}
	if !strings.Contains(report, "target-drift") {
		t.Fatalf("report should name drift reason:\n%s", report)
	}
}

// TestIntegrateSyncsSourceCheckoutWhenTargetIsCurrentBranch verifies the
// H1 fix: when the merge target is the source checkout's current branch,
// `git update-ref` alone leaves the source worktree/index stale; Integrate
// runs ResetHard against the source so a subsequent IsDirty preflight
// does not see false-positive uncommitted changes.
func TestIntegrateSyncsSourceCheckoutWhenTargetIsCurrentBranch(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	g.resolveByRef["main"] = "AAAA"
	g.headByDir["branch:springfield/alpha"] = "BBBB"
	// Source checkout is on main — same as merge target.
	g.currentBranchByDir = map[string]string{root: "main"}

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if !planmerge.IsSuccess(res) {
		t.Fatalf("merge: %+v err=%v", res.Merge, res.Err)
	}
	if res.Merge.SourceSyncStatus != "synced" {
		t.Fatalf("SourceSyncStatus = %q, want synced", res.Merge.SourceSyncStatus)
	}
	if len(g.resetHardCalls) != 1 || g.resetHardCalls[0] != "BBBB" {
		t.Fatalf("expected reset --hard BBBB on source, got %v", g.resetHardCalls)
	}
}

// TestIntegrateSkipsSourceSyncWhenTargetIsDifferentBranch proves that the
// resync only runs when the target IS the source's current HEAD; an
// explicit --ref pointing at a different local branch must not touch the
// source worktree.
func TestIntegrateSkipsSourceSyncWhenTargetIsDifferentBranch(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "release", "AAAA", "BBBB")
	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	g.resolveByRef["release"] = "AAAA"
	g.headByDir["branch:springfield/alpha"] = "BBBB"
	g.currentBranchByDir = map[string]string{root: "main"}

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if !planmerge.IsSuccess(res) {
		t.Fatalf("merge: %+v err=%v", res.Merge, res.Err)
	}
	if res.Merge.SourceSyncStatus != "skipped" {
		t.Fatalf("SourceSyncStatus = %q, want skipped", res.Merge.SourceSyncStatus)
	}
	if len(g.resetHardCalls) != 0 {
		t.Fatalf("source reset must not run when target != current branch: %v", g.resetHardCalls)
	}
}

// TestIntegrateRecordsSourceSyncFailureWithoutDowngradingMerge proves that
// a reset --hard error after a successful merge is recorded as a sync
// warning, not a merge failure: the merge has been published and is
// truthful; the source resync is a separate concern.
func TestIntegrateRecordsSourceSyncFailureWithoutDowngradingMerge(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	g.resolveByRef["main"] = "AAAA"
	g.headByDir["branch:springfield/alpha"] = "BBBB"
	g.currentBranchByDir = map[string]string{root: "main"}
	g.resetHardErr = errors.New("local changes would be overwritten")

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if !planmerge.IsSuccess(res) {
		t.Fatalf("merge must remain succeeded even when resync fails; got %+v err=%v", res.Merge, res.Err)
	}
	if res.Merge.SourceSyncStatus != "failed" {
		t.Fatalf("SourceSyncStatus = %q, want failed", res.Merge.SourceSyncStatus)
	}
	if res.Merge.SourceSyncError == "" {
		t.Fatalf("SourceSyncError should record reset error")
	}
}

// TestIntegrateRefusesResetWhenSourceIsDirty proves the data-loss gate:
// when the source checkout has uncommitted changes (e.g. user edits made
// during the agent run, since the slice-2 dirty preflight only fires
// before execution), Integrate must NOT run `git reset --hard` over
// them. SourceSyncStatus is recorded as "failed" with a descriptive
// error; the publish itself stands.
func TestIntegrateRefusesResetWhenSourceIsDirty(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	g.resolveByRef["main"] = "AAAA"
	g.headByDir["branch:springfield/alpha"] = "BBBB"
	g.currentBranchByDir = map[string]string{root: "main"}
	g.dirtyByDir = map[string]bool{root: true}

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if !planmerge.IsSuccess(res) {
		t.Fatalf("merge should still record success even when resync refused: %+v err=%v", res.Merge, res.Err)
	}
	if res.Merge.SourceSyncStatus != "failed" {
		t.Fatalf("SourceSyncStatus = %q, want failed", res.Merge.SourceSyncStatus)
	}
	if len(g.resetHardCalls) != 0 {
		t.Fatalf("ResetHard must not run against a dirty source: %v", g.resetHardCalls)
	}
}

// TestIsIntegratedFalseWhenMergeSucceededButCleanupNil proves that a
// plan whose merge is durably Succeeded but whose Cleanup ledger was
// never persisted (e.g. cleanup-state-save-failed) must NOT advance the
// queue. Without this gate a transient state.json write failure during
// the cleanup save would let the next start treat the plan as fully
// integrated even though preserved artifacts may still be on disk.
func TestIsIntegratedFalseWhenMergeSucceededButCleanupNil(t *testing.T) {
	st := &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge: &conductor.MergeOutcome{
			Status: conductor.MergeSucceeded,
		},
		Cleanup: nil,
	}
	if st.IsIntegrated() {
		t.Fatalf("MergeSucceeded with Cleanup=nil must not count as integrated")
	}
}

// TestIntegrateRetryAfterDriftPreservesPriorMergeWorktree proves the
// preservation gate: a prior refused merge left .merges/<plan> on disk
// for inspection. A second attempt that ALSO refuses (target still
// drifted) must NOT delete that preserved worktree before the refusal —
// otherwise the operator loses the only artifact the first failure was
// kept around to support.
func TestIntegrateRetryAfterDriftPreservesPriorMergeWorktree(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	prior := project.State.Plans["alpha"]
	prior.PlanHead = "BBBB"
	priorMergeWt := filepath.Join(root, ".worktrees", ".merges", "alpha")
	prior.Merge = &conductor.MergeOutcome{
		Status:       conductor.MergeRefused,
		Mode:         string(planmerge.ModeFFOnly),
		Reason:       planmerge.ReasonTargetDrift,
		TargetRef:    "main",
		WorktreePath: priorMergeWt,
	}
	prior.Cleanup = &conductor.CleanupOutcome{
		Status: conductor.CleanupSkipped,
		MergeWorktree: &conductor.ArtifactCleanup{
			Status: conductor.CleanupPreserved,
			Path:   priorMergeWt,
			Reason: planmerge.ReasonTargetDrift,
		},
	}
	if err := project.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	// Target STILL drifted on retry: refused again.
	g.resolveByRef["main"] = "ZZZZ"

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if res.Reason != planmerge.ReasonTargetDrift {
		t.Fatalf("Reason = %q, want target-drift", res.Reason)
	}
	// The retry refusal must not have touched the preserved merge wt.
	if len(g.worktreeRemoveAll) != 0 {
		t.Fatalf("preserved merge worktree must not be removed before a viable new attempt; got %v", g.worktreeRemoveAll)
	}
	// Cleanup ledger still preserves the merge wt; the path is not lost.
	if res.Cleanup.MergeWorktree == nil || res.Cleanup.MergeWorktree.Path == "" {
		t.Fatalf("preserved merge worktree path lost from cleanup record: %+v", res.Cleanup.MergeWorktree)
	}
}

// TestIsIntegratedFalseWhenSourceSyncFailed proves the queue-correctness
// gate: a plan whose merge succeeded but whose source resync failed must
// not advance, otherwise the next plan trips the dirty-source preflight
// and the queue blames the wrong owner.
func TestIsIntegratedFalseWhenSourceSyncFailed(t *testing.T) {
	st := &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge: &conductor.MergeOutcome{
			Status:           conductor.MergeSucceeded,
			SourceSyncStatus: "failed",
		},
	}
	if st.IsIntegrated() {
		t.Fatalf("plan with failed source sync must not count as integrated")
	}
}

// TestIntegrateRecoversFromUpdateRefSucceededButSaveFailed proves the
// recovery branch: a prior Integrate published via update-ref but failed
// the post-publish state save. The durable record is Pending with
// PostMergeHead recorded (set by the pre-publish save); target_head now
// equals PostMergeHead. The next Integrate must detect this and resume
// (promote merge, run resync+cleanup) instead of refusing with target-
// drift.
func TestIntegrateRecoversFromUpdateRefSucceededButSaveFailed(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	prior := project.State.Plans["alpha"]
	prior.PlanHead = "BBBB"
	prior.Merge = &conductor.MergeOutcome{
		Status:        conductor.MergePending,
		Mode:          string(planmerge.ModeFFOnly),
		TargetRef:     "main",
		TargetHead:    "AAAA",
		PostMergeHead: "BBBB",
		WorktreePath:  filepath.Join(root, ".worktrees", ".merges", "alpha"),
	}
	if err := project.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	// Target main is already at the post-merge head — publish landed
	// last time but state save failed.
	g.resolveByRef["main"] = "BBBB"
	g.currentBranchByDir = map[string]string{root: "main"}

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if !planmerge.IsSuccess(res) {
		t.Fatalf("expected recovery to promote merge to succeeded; got %+v err=%v", res.Merge, res.Err)
	}
	if res.Reason != planmerge.ReasonMergeOK {
		t.Fatalf("Reason = %q, want merge-ok", res.Reason)
	}
	// Recovery must NOT redo update-ref / merge ff — those already happened.
	if len(g.updateRefCalls) != 0 {
		t.Fatalf("update-ref must not re-run on recovery: %v", g.updateRefCalls)
	}
	if len(g.mergeCalls) != 0 {
		t.Fatalf("merge ff must not re-run on recovery: %v", g.mergeCalls)
	}
	if len(g.worktreeAddCalls) != 0 {
		t.Fatalf("merge worktree must not be re-added on recovery: %v", g.worktreeAddCalls)
	}
	// Resync + cleanup DO run.
	if res.Merge.SourceSyncStatus != "synced" {
		t.Fatalf("resync should run during recovery, got %q", res.Merge.SourceSyncStatus)
	}
	if res.Cleanup == nil || res.Cleanup.Status != conductor.CleanupSucceeded {
		t.Fatalf("cleanup should succeed on recovery: %+v", res.Cleanup)
	}
}

// TestPlanStatePendingMergeIsNotIntegrated proves the slice-3 contract
// that a Status=Completed plan whose Merge.Status is Pending (set by
// planrun.SinglePlan to mark "execution done, merge not yet attempted")
// is NOT treated as queue-integrated. A save failure mid-Integrate would
// otherwise leave the durable record looking complete.
func TestPlanStatePendingMergeIsNotIntegrated(t *testing.T) {
	st := &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge:  &conductor.MergeOutcome{Status: conductor.MergePending},
	}
	if st.IsIntegrated() {
		t.Fatalf("Pending merge must not count as integrated")
	}
}

// TestIntegrateReEntryAfterPartialCleanupSkipsAlreadyDeleted proves the
// P1 fix: a prior cleanup run that succeeded on the execution worktree
// and plan branch but failed on the merge worktree must NOT re-attempt
// the already-deleted artifacts on the second run. Re-running git worktree
// remove against an absent path errors out, which would falsely re-fail
// cleanup status forever even after the original blocker was resolved.
func TestIntegrateReEntryAfterPartialCleanupSkipsAlreadyDeletedArtifacts(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	prior := project.State.Plans["alpha"]
	prior.PlanHead = "BBBB"
	prior.Merge = &conductor.MergeOutcome{
		Status:        conductor.MergeSucceeded,
		Mode:          string(planmerge.ModeFFOnly),
		TargetRef:     "main",
		TargetHead:    "AAAA",
		PostMergeHead: "BBBB",
		WorktreePath:  filepath.Join(root, ".worktrees", ".merges", "alpha"),
	}
	prior.Cleanup = &conductor.CleanupOutcome{
		Status: conductor.CleanupFailed,
		MergeWorktree: &conductor.ArtifactCleanup{
			Status: conductor.CleanupFailed,
			Path:   prior.Merge.WorktreePath,
			Error:  "busy",
		},
		// Execution worktree + plan branch DELETED on prior run.
		ExecutionWorktree: &conductor.ArtifactCleanup{Status: conductor.CleanupSucceeded, Path: wt},
		PlanBranch:        &conductor.ArtifactCleanup{Status: conductor.CleanupSucceeded, Branch: "springfield/alpha"},
	}
	if err := project.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	g.resolveByRef["main"] = "BBBB"
	g.currentBranchByDir = map[string]string{root: "main"}
	// Real git would now fail on second worktree-remove and second
	// branch-delete because the prior run already deleted them — simulate
	// that.
	g.worktreeRemoveErr[wt] = errors.New("not a working tree")
	g.branchDeleteErr = errors.New("branch not found")

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if res.Err != nil {
		t.Fatalf("re-entry returned err: %v", res.Err)
	}
	// Re-entry retried only the merge worktree (the failing artifact).
	// Execution worktree + plan branch must NOT have been re-attempted
	// since prior status was Succeeded.
	if len(g.worktreeRemoveAll) != 1 || g.worktreeRemoveAll[0] != prior.Merge.WorktreePath {
		t.Fatalf("expected only merge wt remove on re-entry, got %v", g.worktreeRemoveAll)
	}
	if len(g.branchDeleteCalls) != 0 {
		t.Fatalf("plan branch delete must not re-run when prior status was succeeded, got %v", g.branchDeleteCalls)
	}
	// With merge wt now removable on re-entry, cleanup converges to
	// succeeded (every artifact in succeeded state).
	if res.Cleanup.Status != conductor.CleanupSucceeded {
		t.Fatalf("re-entry cleanup should converge to succeeded, got %s", res.Cleanup.Status)
	}
	if res.Cleanup.ExecutionWorktree.Status != conductor.CleanupSucceeded {
		t.Fatalf("execution wt status carried wrong: %+v", res.Cleanup.ExecutionWorktree)
	}
	if res.Cleanup.PlanBranch.Status != conductor.CleanupSucceeded {
		t.Fatalf("plan branch status carried wrong: %+v", res.Cleanup.PlanBranch)
	}
}

// TestIntegrateAbortsBeforePublishOnSaveFailure verifies that when the
// pre-publish state save fails, the merge phase aborts BEFORE running
// update-ref. Cleanup must not run; the publish must not happen; the
// merge worktree may or may not exist on disk, but the source target ref
// is unchanged. This protects the slice-3 contract that a save failure
// either commits the merge fully (with PostMergeHead persisted for
// recovery) or leaves the source repo untouched.
func TestIntegrateAbortsBeforePublishOnSaveFailure(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	g.resolveByRef["main"] = "AAAA"
	g.headByDir["branch:springfield/alpha"] = "BBBB"
	g.currentBranchByDir = map[string]string{root: "main"}
	// Sabotage state.json so any SaveState call fails.
	sabotageStateJSON(t, root)

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if res.Err == nil && res.Reason != planmerge.ReasonMergeFailed {
		t.Fatalf("expected merge-failed outcome from save failure; got %+v", res)
	}
	// Critical: update-ref must NOT have run when the pre-publish save
	// failed. Otherwise the source target advances without a durable
	// record.
	if len(g.updateRefCalls) != 0 {
		t.Fatalf("update-ref must not run before pre-publish save succeeds; got %v", g.updateRefCalls)
	}
	if len(g.worktreeRemoveAll) != 0 {
		t.Fatalf("cleanup must not delete worktrees when save fails: %v", g.worktreeRemoveAll)
	}
	if len(g.branchDeleteCalls) != 0 {
		t.Fatalf("cleanup must not delete plan branch when save fails: %v", g.branchDeleteCalls)
	}
}

// sabotageStateJSON makes Project.SaveState fail by replacing state.json
// with a directory of the same name.
func sabotageStateJSON(t *testing.T, root string) {
	t.Helper()
	statePath := filepath.Join(root, ".springfield", "execution", "state.json")
	_ = os.Remove(statePath)
	if err := os.MkdirAll(statePath, 0o755); err != nil {
		t.Fatalf("sabotage state.json: %v", err)
	}
}

// TestIntegrateReEntryAfterMergeSuccessRunsOnlyCleanup proves the
// idempotent re-entry path: a prior attempt left Merge.Status=Succeeded
// with Cleanup.Status=Failed; a second Integrate must NOT redo the merge
// (it would falsely flag drift since the target now equals post_merge_head)
// and must run only the cleanup matrix again.
func TestIntegrateReEntryAfterMergeSuccessRunsOnlyCleanup(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	prior := project.State.Plans["alpha"]
	prior.PlanHead = "BBBB"
	prior.Merge = &conductor.MergeOutcome{
		Status:        conductor.MergeSucceeded,
		Mode:          string(planmerge.ModeFFOnly),
		TargetRef:     "main",
		TargetHead:    "AAAA",
		PostMergeHead: "BBBB",
		WorktreePath:  filepath.Join(root, ".worktrees", ".merges", "alpha"),
	}
	prior.Cleanup = &conductor.CleanupOutcome{
		Status: conductor.CleanupFailed,
		MergeWorktree: &conductor.ArtifactCleanup{
			Status: conductor.CleanupFailed,
			Path:   prior.Merge.WorktreePath,
			Error:  "busy",
		},
		ExecutionWorktree: &conductor.ArtifactCleanup{Status: conductor.CleanupSucceeded, Path: wt},
		PlanBranch:        &conductor.ArtifactCleanup{Status: conductor.CleanupSucceeded, Branch: "springfield/alpha"},
	}
	if err := project.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	g := newFakeGit()
	// Target main is now at the post-merge head — a re-merge would falsely
	// observe drift. The fake records every git op so we can assert no
	// merge-side ops fire.
	g.headByDir[wt] = "BBBB"
	g.resolveByRef["main"] = "BBBB"

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if res.Err != nil {
		t.Fatalf("Integrate re-entry: %v", res.Err)
	}
	if !planmerge.IsSuccess(res) {
		t.Fatalf("expected merge success preserved, got %+v", res.Merge)
	}
	if len(g.worktreeAddCalls) != 0 || len(g.mergeCalls) != 0 || len(g.updateRefCalls) != 0 {
		t.Fatalf("re-entry must not redo merge ops: add=%v merge=%v ref=%v",
			g.worktreeAddCalls, g.mergeCalls, g.updateRefCalls)
	}
	if len(g.worktreeRemoveAll) == 0 && len(g.branchDeleteCalls) == 0 {
		t.Fatalf("re-entry must drive cleanup matrix; saw no removals")
	}
	// Cleanup status reflects fresh attempt — fake has no remove errors set
	// this time, so cleanup should now succeed.
	if res.Cleanup.Status != conductor.CleanupSucceeded {
		t.Fatalf("re-entry cleanup status = %s, want succeeded", res.Cleanup.Status)
	}
}

// TestIntegrateReEntryAfterRefusalRemovesPriorMergeWorktree proves the
// idempotent re-entry path for a previously-failed merge: a leftover merge
// worktree from the prior attempt is best-effort removed before the new
// `git worktree add` so the second attempt does not get blocked by the
// stale registration.
func TestIntegrateReEntryAfterFailureRemovesPriorMergeWorktree(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	prior := project.State.Plans["alpha"]
	leftover := filepath.Join(root, ".worktrees", ".merges", "alpha")
	prior.Merge = &conductor.MergeOutcome{
		Status:       conductor.MergeFailed,
		Mode:         string(planmerge.ModeFFOnly),
		Reason:       planmerge.ReasonFFNotPossible,
		TargetRef:    "main",
		TargetHead:   "AAAA",
		WorktreePath: leftover,
	}
	prior.Cleanup = &conductor.CleanupOutcome{Status: conductor.CleanupSkipped}
	if err := project.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	g.resolveByRef["main"] = "AAAA"
	g.headByDir["branch:springfield/alpha"] = "BBBB"

	res := planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})
	if !planmerge.IsSuccess(res) {
		t.Fatalf("expected merge to succeed on re-entry, got %+v err=%v", res.Merge, res.Err)
	}
	// Best-effort removal of leftover happens before new worktree add.
	if len(g.worktreeRemoveAll) < 1 || g.worktreeRemoveAll[0] != leftover {
		t.Fatalf("expected leftover merge worktree removal first; got %v", g.worktreeRemoveAll)
	}
	if len(g.worktreeAddCalls) != 1 {
		t.Fatalf("expected exactly one fresh merge worktree add, got %v", g.worktreeAddCalls)
	}
}

func TestStatusSurfacesPreservedExecutionWorktree(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "main", "AAAA", "BBBB")
	g := newFakeGit()
	g.headByDir[wt] = "BBBB"
	g.resolveByRef["main"] = "ZZZZ"
	planmerge.Integrate(planmerge.IntegrateInput{
		Project: project, PlanID: "alpha", ControlRoot: root, WorktreeBase: ".worktrees", Git: g,
	})

	rs := conductor.BuildRegistryStatus(project)
	out := rs.Render()
	if !strings.Contains(out, "merge: refused") {
		t.Fatalf("status missing merge refusal:\n%s", out)
	}
	if !strings.Contains(out, "execution worktree preserved") {
		t.Fatalf("status missing preserved execution worktree:\n%s", out)
	}
	if !strings.Contains(out, "plan branch preserved") {
		t.Fatalf("status missing preserved plan branch:\n%s", out)
	}
}
