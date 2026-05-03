package planrun_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planrun"
)

func TestPlanKeyMatchesSanitizedID(t *testing.T) {
	got := planrun.PlanKey(conductor.PlanUnit{ID: "feature-a"})
	if got != "feature-a" {
		t.Fatalf("PlanKey: got %q want %q", got, "feature-a")
	}
}

func TestWorktreePathIsControlRootRelative(t *testing.T) {
	root := "/tmp/proj"
	got, err := planrun.WorktreePath(root, ".worktrees", conductor.PlanUnit{ID: "feature-a"}, nil)
	if err != nil {
		t.Fatalf("WorktreePath: %v", err)
	}
	want := filepath.Join(root, ".worktrees", "feature-a")
	if got != want {
		t.Fatalf("WorktreePath: got %q want %q", got, want)
	}
}

func TestWorktreePathDefaultsBaseWhenEmpty(t *testing.T) {
	got, err := planrun.WorktreePath("/tmp/proj", "", conductor.PlanUnit{ID: "x"}, nil)
	if err != nil {
		t.Fatalf("WorktreePath: %v", err)
	}
	if !strings.Contains(got, ".worktrees") {
		t.Fatalf("default base lost: %q", got)
	}
}

func TestWorktreePathAvoidsCollisionWithSibling(t *testing.T) {
	root := "/tmp/proj"
	siblingPath := filepath.Join(root, ".worktrees", "feature-a")
	existing := map[string]string{
		"feature-a-original": siblingPath,
	}
	got, err := planrun.WorktreePath(root, ".worktrees", conductor.PlanUnit{ID: "feature-a"}, existing)
	if err != nil {
		t.Fatalf("WorktreePath: %v", err)
	}
	if got == siblingPath {
		t.Fatalf("collision not resolved: got %q (matches sibling)", got)
	}
	if !strings.HasPrefix(filepath.Base(got), "feature-a-") {
		t.Fatalf("expected suffixed key, got %q", got)
	}
}

func TestWorktreePathSameOwnerKeepsPath(t *testing.T) {
	root := "/tmp/proj"
	canonical := filepath.Join(root, ".worktrees", "feature-a")
	existing := map[string]string{"feature-a": canonical}
	got, err := planrun.WorktreePath(root, ".worktrees", conductor.PlanUnit{ID: "feature-a"}, existing)
	if err != nil {
		t.Fatalf("WorktreePath: %v", err)
	}
	if got != canonical {
		t.Fatalf("same-owner path drift: got %q want %q", got, canonical)
	}
}

func TestBranchNameDefaultNamespaced(t *testing.T) {
	got := planrun.BranchName(conductor.PlanUnit{ID: "feature-a"})
	if got != "springfield/feature-a" {
		t.Fatalf("BranchName default: got %q", got)
	}
}

func TestBranchNameUsesPlanBranchOverride(t *testing.T) {
	got := planrun.BranchName(conductor.PlanUnit{ID: "feature-a", PlanBranch: "feat/login"})
	if got != "feat/login" {
		t.Fatalf("BranchName override lost: got %q", got)
	}
}

func TestInputDigestStableWhenInputsUnchanged(t *testing.T) {
	root := t.TempDir()
	planRel := "springfield/plans/p.md"
	mustWrite(t, filepath.Join(root, planRel), "plan body")
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "agents body")

	unit := conductor.PlanUnit{ID: "p", Path: planRel}
	a, err := planrun.InputDigest(root, unit)
	if err != nil {
		t.Fatalf("InputDigest: %v", err)
	}
	b, err := planrun.InputDigest(root, unit)
	if err != nil {
		t.Fatalf("InputDigest: %v", err)
	}
	if a != b {
		t.Fatalf("digest unstable: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "sha256:") {
		t.Fatalf("digest format: %q", a)
	}
}

func TestInputDigestChangesWhenPlanFileChanges(t *testing.T) {
	root := t.TempDir()
	planRel := "springfield/plans/p.md"
	mustWrite(t, filepath.Join(root, planRel), "plan body v1")

	unit := conductor.PlanUnit{ID: "p", Path: planRel}
	v1, err := planrun.InputDigest(root, unit)
	if err != nil {
		t.Fatalf("InputDigest v1: %v", err)
	}
	mustWrite(t, filepath.Join(root, planRel), "plan body v2 different")
	v2, err := planrun.InputDigest(root, unit)
	if err != nil {
		t.Fatalf("InputDigest v2: %v", err)
	}
	if v1 == v2 {
		t.Fatalf("digest did not change after plan body edit")
	}
}

func TestInputDigestChangesWhenGuidanceAdded(t *testing.T) {
	root := t.TempDir()
	planRel := "springfield/plans/p.md"
	mustWrite(t, filepath.Join(root, planRel), "plan")
	unit := conductor.PlanUnit{ID: "p", Path: planRel}
	before, err := planrun.InputDigest(root, unit)
	if err != nil {
		t.Fatalf("InputDigest before: %v", err)
	}
	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "new guidance")
	after, err := planrun.InputDigest(root, unit)
	if err != nil {
		t.Fatalf("InputDigest after: %v", err)
	}
	if before == after {
		t.Fatalf("digest did not change after guidance add")
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
