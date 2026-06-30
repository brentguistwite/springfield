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
	d, err := resolveBatchModeAndBase(g, "/r", batch.Run{}, true, "develop", config.Config{}, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !d.PerPlan || !d.Stamp || d.Mode != "per-plan" || d.BatchBase != "develop" {
		t.Fatalf("fresh per-plan via flag wrong: %+v", d)
	}
}

func TestResolveModeFreshConsolidateDefault(t *testing.T) {
	g := fakeBaseGit{branch: "main"}
	d, err := resolveBatchModeAndBase(g, "/r", batch.Run{}, false, "", config.Config{}, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if d.PerPlan || !d.Stamp || d.Mode != "consolidate" || d.BatchBase != "" {
		t.Fatalf("fresh consolidate wrong: %+v", d)
	}
}

func TestResolveModeFreshPerPlanViaConfig(t *testing.T) {
	g := fakeBaseGit{branch: "main"}
	d, err := resolveBatchModeAndBase(g, "/r", batch.Run{}, false, "", perPlanConfig(), false)
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
	d, err := resolveBatchModeAndBase(g, "/r", run, false, "", config.Config{}, false)
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
	d, err := resolveBatchModeAndBase(g, "/r", run, false, "new-base", config.Config{}, false)
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
	d, err := resolveBatchModeAndBase(g, "/r", run, true, "", config.Config{}, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if d.PerPlan {
		t.Fatalf("stamped consolidate must not flip to per-plan on resume: %+v", d)
	}
}

// A requested per-plan mode that gets overridden by an unstamped-but-in-progress
// batch must be surfaced (SuppressedPerPlanRequest) so the caller can warn —
// otherwise --per-plan-branches is silently dropped. True whether the request
// came from the flag or [project] branch_mode.
func TestResolveModeLegacyInProgressFlagSetsSuppressedFlag(t *testing.T) {
	g := fakeBaseGit{branch: "main"}
	d, err := resolveBatchModeAndBase(g, "/r", batch.Run{}, true, "", config.Config{}, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !d.SuppressedPerPlanRequest {
		t.Fatalf("a dropped --per-plan-branches request must set SuppressedPerPlanRequest, got %+v", d)
	}
}

func TestResolveModeLegacyInProgressConfigSetsSuppressedFlag(t *testing.T) {
	g := fakeBaseGit{branch: "main"}
	d, err := resolveBatchModeAndBase(g, "/r", batch.Run{}, false, "", perPlanConfig(), true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !d.SuppressedPerPlanRequest {
		t.Fatalf("a dropped [project] per-plan request must set SuppressedPerPlanRequest, got %+v", d)
	}
}

// No request → nothing suppressed; a legacy consolidate resume without any
// per-plan ask must NOT warn.
func TestResolveModeLegacyInProgressNoRequestNoSuppression(t *testing.T) {
	g := fakeBaseGit{branch: "main"}
	d, err := resolveBatchModeAndBase(g, "/r", batch.Run{}, false, "", config.Config{}, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if d.SuppressedPerPlanRequest {
		t.Fatalf("no per-plan request must not set SuppressedPerPlanRequest, got %+v", d)
	}
}

// A genuinely-honored per-plan request (fresh, no prior progress) must NOT set
// the suppressed flag — the flag is specifically the "asked-but-denied" signal.
func TestResolveModeFreshPerPlanNotSuppressed(t *testing.T) {
	g := fakeBaseGit{branch: "main"}
	d, err := resolveBatchModeAndBase(g, "/r", batch.Run{}, true, "", config.Config{}, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if d.SuppressedPerPlanRequest {
		t.Fatalf("an honored per-plan request must not set SuppressedPerPlanRequest, got %+v", d)
	}
}

// A stamped CONSOLIDATE batch resumed with --per-plan-branches must also flag
// the dropped request (symmetry with the unstamped-in-progress path) so the
// caller warns instead of silently ignoring the flag.
func TestResolveModeStampedConsolidateFlagSetsSuppressed(t *testing.T) {
	g := fakeBaseGit{branch: "main"}
	run := batch.Run{BatchMode: "consolidate"}
	d, err := resolveBatchModeAndBase(g, "/r", run, true, "", config.Config{}, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if d.PerPlan {
		t.Fatalf("stamped consolidate must not flip to per-plan, got %+v", d)
	}
	if !d.SuppressedPerPlanRequest {
		t.Fatalf("a dropped per-plan request on a stamped consolidate batch must set SuppressedPerPlanRequest, got %+v", d)
	}
}

// A stamped PER-PLAN resume with the flag re-passed is honored, not suppressed.
func TestResolveModeStampedPerPlanFlagNotSuppressed(t *testing.T) {
	g := fakeBaseGit{branch: "main"}
	run := batch.Run{BatchMode: "per-plan", BatchBase: "develop"}
	d, err := resolveBatchModeAndBase(g, "/r", run, true, "", config.Config{}, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !d.PerPlan || d.SuppressedPerPlanRequest {
		t.Fatalf("honored per-plan resume must not suppress, got %+v", d)
	}
}

func TestResolveModePerPlanDetachedHeadRejected(t *testing.T) {
	g := fakeBaseGit{detached: true}
	_, err := resolveBatchModeAndBase(g, "/r", batch.Run{}, true, "", config.Config{}, false)
	if err == nil {
		t.Fatal("per-plan with detached HEAD and no base must be rejected")
	}
}

func TestResolveModeConsolidateSkipsBaseResolution(t *testing.T) {
	// Detached HEAD must NOT matter in consolidate mode — base is resolved
	// per-plan (post-autobranch) as before, not here.
	g := fakeBaseGit{detached: true}
	d, err := resolveBatchModeAndBase(g, "/r", batch.Run{}, false, "", config.Config{}, false)
	if err != nil {
		t.Fatalf("consolidate must not resolve base (got err %v)", err)
	}
	if d.BatchBase != "" {
		t.Fatalf("consolidate BatchBase must stay empty, got %q", d.BatchBase)
	}
}

// Pre-feature in-progress batch (unstamped + has progress) must NOT flip to
// per-plan when --per-plan-branches is passed on resume.
func TestResolveModeLegacyInProgressLocksConsolidate(t *testing.T) {
	g := fakeBaseGit{branch: "main"}
	d, err := resolveBatchModeAndBase(g, "/r", batch.Run{}, true, "", config.Config{}, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if d.PerPlan {
		t.Fatalf("legacy in-progress batch must lock consolidate, got %+v", d)
	}
	if !d.Stamp || d.Mode != "consolidate" {
		t.Fatalf("must stamp consolidate to lock the mode, got %+v", d)
	}
}
