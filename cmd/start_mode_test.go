package cmd

import (
	"errors"
	"testing"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
)

// fakeBaseGit is a minimal planrun.Git double: only CurrentBranch is exercised
// by resolveBatchModeAndBase; the rest satisfy the interface.
type fakeBaseGit struct {
	branch   string
	detached bool
}

func (f fakeBaseGit) CurrentBranch(string) (string, error) {
	if f.detached {
		return "", errors.New("detached HEAD")
	}
	return f.branch, nil
}
func (fakeBaseGit) IsRepo(string) (bool, error)                    { return true, nil }
func (fakeBaseGit) IsDirty(string) (bool, error)                   { return false, nil }
func (fakeBaseGit) ResolveRef(_, ref string) (string, error)       { return "sha", nil }
func (fakeBaseGit) BranchExists(_, _ string) (bool, error)         { return true, nil }
func (fakeBaseGit) WorktreeListPaths(string) ([]string, error)     { return nil, nil }
func (fakeBaseGit) WorktreeAddNewBranch(_, _, _, _ string) error   { return nil }
func (fakeBaseGit) WorktreeAddExistingBranch(_, _, _ string) error { return nil }
func (fakeBaseGit) Head(string) (string, error)                    { return "sha", nil }
func (fakeBaseGit) Diff(_, _ string) (string, error)               { return "", nil }

func perPlanConfig() config.Config {
	return config.Config{Project: config.ProjectConfig{BranchMode: "per-plan"}}
}

func TestResolveModeFreshPerPlanViaFlagStamps(t *testing.T) {
	g := fakeBaseGit{branch: "main"}
	d, err := resolveBatchModeAndBase(g, "/r", batch.Run{}, true, "develop", config.Config{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !d.PerPlan || !d.Stamp || d.Mode != "per-plan" || d.BatchBase != "develop" {
		t.Fatalf("fresh per-plan via flag wrong: %+v", d)
	}
}

func TestResolveModeFreshConsolidateDefault(t *testing.T) {
	g := fakeBaseGit{branch: "main"}
	d, err := resolveBatchModeAndBase(g, "/r", batch.Run{}, false, "", config.Config{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if d.PerPlan || !d.Stamp || d.Mode != "consolidate" || d.BatchBase != "" {
		t.Fatalf("fresh consolidate wrong: %+v", d)
	}
}

func TestResolveModeFreshPerPlanViaConfig(t *testing.T) {
	g := fakeBaseGit{branch: "main"}
	d, err := resolveBatchModeAndBase(g, "/r", batch.Run{}, false, "", perPlanConfig())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !d.PerPlan || d.Mode != "per-plan" {
		t.Fatalf("config per-plan must be honored: %+v", d)
	}
	if d.BatchBase != "main" {
		t.Fatalf("per-plan base must fall back to current branch, got %q", d.BatchBase)
	}
}

// (a) resume without flag stays per-plan.
func TestResolveModeResumeStaysPerPlan(t *testing.T) {
	g := fakeBaseGit{branch: "main"}
	run := batch.Run{BatchMode: "per-plan", BatchBase: "develop"}
	d, err := resolveBatchModeAndBase(g, "/r", run, false, "", config.Config{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !d.PerPlan {
		t.Fatalf("resume must stay per-plan: %+v", d)
	}
	if d.Stamp {
		t.Fatalf("resume must never re-stamp: %+v", d)
	}
	if d.BatchBase != "develop" {
		t.Fatalf("resume base should come from BatchBase, got %q", d.BatchBase)
	}
}

// (b) resume re-passing --base overrides for pending plans; Stamp stays false
// so the on-disk BatchBase is left unchanged by the caller.
func TestResolveModeResumeRebaseOverridesWithoutStamp(t *testing.T) {
	g := fakeBaseGit{branch: "main"}
	run := batch.Run{BatchMode: "per-plan", BatchBase: "old-base"}
	d, err := resolveBatchModeAndBase(g, "/r", run, false, "new-base", config.Config{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if d.BatchBase != "new-base" {
		t.Fatalf("re-passed --base must override threaded base, got %q", d.BatchBase)
	}
	if d.Stamp {
		t.Fatalf("resume must not stamp; on-disk BatchBase must stay old-base")
	}
}

// (c) resume re-passing --per-plan-branches does NOT flip a consolidate batch.
func TestResolveModeResumeFlagDoesNotFlipStampedMode(t *testing.T) {
	g := fakeBaseGit{branch: "main"}
	run := batch.Run{BatchMode: "consolidate"}
	d, err := resolveBatchModeAndBase(g, "/r", run, true, "", config.Config{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if d.PerPlan {
		t.Fatalf("stamped consolidate must not flip to per-plan on resume: %+v", d)
	}
}

func TestResolveModePerPlanDetachedHeadRejected(t *testing.T) {
	g := fakeBaseGit{detached: true}
	_, err := resolveBatchModeAndBase(g, "/r", batch.Run{}, true, "", config.Config{})
	if err == nil {
		t.Fatal("per-plan with detached HEAD and no base must be rejected")
	}
}

func TestResolveModeConsolidateSkipsBaseResolution(t *testing.T) {
	// Detached HEAD must NOT matter in consolidate mode — base is resolved
	// per-plan (post-autobranch) as before, not here.
	g := fakeBaseGit{detached: true}
	d, err := resolveBatchModeAndBase(g, "/r", batch.Run{}, false, "", config.Config{})
	if err != nil {
		t.Fatalf("consolidate must not resolve base (got err %v)", err)
	}
	if d.BatchBase != "" {
		t.Fatalf("consolidate BatchBase must stay empty, got %q", d.BatchBase)
	}
}
