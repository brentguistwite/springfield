package execution

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"springfield/internal/core/lock"
	"springfield/internal/features/conductor"
	"springfield/internal/features/prd"
)

// BatchLiveness reports whether a live springfield process owns the
// control-plane lock, plus the stale `running` plan markers a recover would
// (or did) reset. It distinguishes a genuinely-running batch from one whose
// owning process died without recording a terminal result (dogfood #10), which
// the batch.json-presence check alone cannot tell apart.
type BatchLiveness struct {
	// Holder is non-nil when a live springfield process currently holds the
	// lock. When set, the batch is genuinely running and nothing is cleared.
	Holder *lock.ErrLockHeld
	// StaleRunning lists plans persisted as running, surfaced read-only for the
	// diagnose path. Populated only when no live Holder exists.
	StaleRunning []string
	// Cleared lists plans this call normalized from running to interrupted.
	// Non-empty only when clear is true and no live Holder exists.
	Cleared []string
	// LockUnreadable is true when the control-plane lock file existed but could
	// not be read to confirm a holder (torn write, permission error). The batch
	// is treated as not-confirmed-live so recovery can proceed, but the caller
	// should surface this so an operator can rule out a genuinely live run.
	LockUnreadable bool
}

// ResolveActiveBatchLiveness probes process liveness of the active batch's
// owner via the control-plane flock. When a live process holds the lock it
// returns the Holder and touches nothing. When no live process holds it
// (process-dead orphan), it lists the stale running plans; if clear is true it
// also normalizes them to interrupted and persists, returning them in Cleared.
// clear=false is the read-only diagnose path.
func ResolveActiveBatchLiveness(rootDir string, clear bool) (BatchLiveness, error) {
	// A CONFIRMED holder (PID != 0) means a live process owns the batch. A
	// non-nil holder with PID 0 is lock.Inspect's "held-but-unreadable / open
	// failed" sentinel — NOT proof of a live process. Treating it as live would
	// block recovery on exactly the torn/permission-degraded .lock a crash can
	// leave behind, so we fall through to stale-running handling and flag the
	// unreadable lock for the caller to surface.
	held := lock.Inspect(rootDir)
	if held != nil && held.PID != 0 {
		return BatchLiveness{Holder: held}, nil
	}
	lockUnreadable := held != nil && held.PID == 0

	project, err := conductor.LoadProject(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return BatchLiveness{}, nil
		}
		return BatchLiveness{}, err
	}

	var stale []string
	for id, plan := range project.State.Plans {
		if plan != nil && plan.Status == conductor.StatusRunning {
			stale = append(stale, id)
		}
	}
	sort.Strings(stale)

	if !clear {
		return BatchLiveness{StaleRunning: stale, LockUnreadable: lockUnreadable}, nil
	}

	cleared := project.NormalizeStaleRunning(time.Now())
	if len(cleared) > 0 {
		if err := project.SaveState(); err != nil {
			return BatchLiveness{}, fmt.Errorf("persist stale-running normalization: %w", err)
		}
	}
	return BatchLiveness{StaleRunning: stale, Cleared: cleared, LockUnreadable: lockUnreadable}, nil
}

// DiagnosePlan loads project state, normalizes stale-running plans, inspects
// the plan's worktree if one is recorded, and returns a full diagnosis.
func DiagnosePlan(rootDir, planID string) (*conductor.PlanDiagnosis, error) {
	project, err := conductor.LoadProject(rootDir)
	if err != nil {
		return nil, err
	}
	if changed := project.NormalizeStaleRunning(time.Now()); len(changed) > 0 {
		if err := project.SaveState(); err != nil {
			return nil, err
		}
	}

	// Validate plan exists in config
	found := false
	for _, name := range project.AllPlans() {
		if name == planID {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("plan %q is not registered in the execution config", planID)
	}

	var wt *conductor.WorktreeInspection
	if ps, ok := project.State.Plans[planID]; ok && ps != nil && ps.WorktreePath != "" {
		wt = inspectWorktree(rootDir, ps.WorktreePath, ps.BaseHead)
	}

	return conductor.DiagnosePlan(project, planID, wt), nil
}

// RecoverPlan performs the named recovery action on a plan and persists the
// result. Returns the recorded recovery action.
func RecoverPlan(rootDir, planID, action string) (*conductor.RecoveryAction, error) {
	project, err := conductor.LoadProject(rootDir)
	if err != nil {
		return nil, err
	}
	if changed := project.NormalizeStaleRunning(time.Now()); len(changed) > 0 {
		if err := project.SaveState(); err != nil {
			return nil, err
		}
	}

	var rec *conductor.RecoveryAction
	switch action {
	case "retry":
		rec, err = project.RecoverRetry(planID)
	case "retry-merge":
		rec, err = project.RecoverRetryMerge(planID)
	case "retry-integration":
		rec, err = project.RecoverRetryIntegration(planID)
	default:
		return nil, fmt.Errorf("unknown recovery action %q; available: retry, retry-merge, retry-integration", action)
	}
	if err != nil {
		return nil, err
	}
	if err := project.SaveState(); err != nil {
		return nil, fmt.Errorf("persist recovery: %w", err)
	}
	return rec, nil
}

// MarkPlanCompleted flips a non-completed plan to StatusCompleted (A9) after
// validating that every story in the plan's prd.json passes, then queues a
// pending merge for the next springfield start to integrate. Reads the plan's
// prd.json from the registered plan-unit path and persists the result.
func MarkPlanCompleted(rootDir, planID string) (*conductor.RecoveryAction, error) {
	project, err := conductor.LoadProject(rootDir)
	if err != nil {
		return nil, err
	}

	unit, ok := project.PlanUnitByID(planID)
	if !ok {
		return nil, fmt.Errorf("plan %q is not registered in the execution config", planID)
	}
	if filepath.Base(unit.Path) != "prd.json" {
		return nil, fmt.Errorf("plan %q is a legacy plan (path %q); --mark-completed requires a prd.json plan", planID, unit.Path)
	}

	prdPath := filepath.Join(rootDir, filepath.FromSlash(unit.Path))
	plan, err := prd.ParseFile(prdPath)
	if err != nil {
		return nil, fmt.Errorf("load prd for plan %q: %w", planID, err)
	}

	rec, err := project.MarkPlanCompleted(planID, plan.UserStories)
	if err != nil {
		return nil, err
	}
	if err := project.SaveState(); err != nil {
		return nil, fmt.Errorf("persist mark-completed: %w", err)
	}
	return rec, nil
}

// AcceptPlanDrift is the operator escape hatch (A10) for deliberate input
// changes. It recomputes the plan's input digest from current inputs, records
// it, and resets the plan to pending so the next springfield start no longer
// refuses with preflight-input-drift.
//
// computeDigest is injected rather than called directly: the canonical
// InputDigest lives in planrun, which imports execution, so execution cannot
// import planrun without a cycle. The cmd layer (above both) supplies a closure
// over planrun.InputDigest.
func AcceptPlanDrift(rootDir, planID string, computeDigest func(conductor.PlanUnit) (string, error)) (*conductor.RecoveryAction, error) {
	project, err := conductor.LoadProject(rootDir)
	if err != nil {
		return nil, err
	}

	unit, ok := project.PlanUnitByID(planID)
	if !ok {
		return nil, fmt.Errorf("plan %q is not registered in the execution config", planID)
	}

	digest, err := computeDigest(unit)
	if err != nil {
		return nil, fmt.Errorf("compute input digest for plan %q: %w", planID, err)
	}

	rec, err := project.AcceptInputDrift(planID, digest)
	if err != nil {
		return nil, err
	}
	if err := project.SaveState(); err != nil {
		return nil, fmt.Errorf("persist accept-drift: %w", err)
	}
	return rec, nil
}

// ResetPlan discards a prior attempt: it removes the recorded worktree and
// deletes the plan branch, then resets the plan to a clean first-run via
// Project.ResetPlanFresh. This is the full-cleanup path for dogfood #6 —
// removing only the worktree (as the preflight-input-drift message used to
// suggest) left the springfield/<plan> branch and a dangling worktree
// registration behind, colliding with the next worktree add. Branch deletion
// is the load-bearing step and surfaces real errors; worktree removal is
// best-effort + prune so a manually-deleted worktree dir still gets cleaned.
func ResetPlan(rootDir, planID string) (*conductor.RecoveryAction, error) {
	project, err := conductor.LoadProject(rootDir)
	if err != nil {
		return nil, err
	}
	if _, ok := project.PlanUnitByID(planID); !ok {
		return nil, fmt.Errorf("plan %q is not registered in the execution config", planID)
	}

	ps, ok := project.State.Plans[planID]
	if !ok || ps == nil {
		// Registered but never run: there is no worktree, branch, or state to
		// clean — the plan is already in a clean first-run state.
		return &conductor.RecoveryAction{Action: "reset", Reason: "plan has no prior attempt; already clean"}, nil
	}
	worktreePath, branch := ps.WorktreePath, ps.Branch

	// Validate BEFORE any git mutation. ResetPlanFresh refuses a running (and a
	// fully-integrated) plan; running it first means a `--reset` on a live run
	// is rejected before cleanupPlanArtifacts could delete that run's worktree
	// and branch. It mutates state only in memory here — SaveState below is
	// what persists, and only after cleanup succeeds (so a cleanup failure
	// leaves on-disk state untouched).
	rec, err := project.ResetPlanFresh(planID)
	if err != nil {
		return nil, err
	}
	if err := cleanupPlanArtifacts(rootDir, worktreePath, branch); err != nil {
		return nil, err
	}
	if err := project.SaveState(); err != nil {
		return nil, fmt.Errorf("persist reset: %w", err)
	}
	return rec, nil
}

// cleanupPlanArtifacts removes the on-disk worktree and the plan branch so a
// reset plan can be re-created from base without colliding with stale git
// state. Worktree removal is best-effort (a manually-deleted dir is healed by
// prune); branch deletion propagates errors since a surviving branch is the
// exact collision dogfood #6 reported.
func cleanupPlanArtifacts(rootDir, worktreePath, branch string) error {
	if worktreePath != "" {
		// Best-effort: a manually-deleted worktree dir makes `worktree remove`
		// fail, but prune below clears its stale registration. The post-cleanup
		// check is what catches a genuine failure (e.g. a locked worktree).
		_, _ = exec.Command("git", "-C", rootDir, "worktree", "remove", "--force", worktreePath).CombinedOutput()
	}
	_, _ = exec.Command("git", "-C", rootDir, "worktree", "prune").CombinedOutput()
	// Verify the registration is actually gone. If it survived both remove and
	// prune, the next `worktree add` will still collide — the exact failure
	// reset exists to prevent — so surface it instead of reporting success.
	if worktreePath != "" && isWorktreeRegistered(rootDir, worktreePath) {
		return fmt.Errorf("worktree %s is still registered after remove+prune; resolve it manually (e.g. `git worktree remove --force`) before retrying reset", worktreePath)
	}
	if branch != "" && localBranchExists(rootDir, branch) {
		if out, err := exec.Command("git", "-C", rootDir, "branch", "-D", branch).CombinedOutput(); err != nil {
			return fmt.Errorf("delete plan branch %q: %w: %s", branch, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func localBranchExists(rootDir, branch string) bool {
	return exec.Command("git", "-C", rootDir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}

func inspectWorktree(rootDir, worktreePath, baseHead string) *conductor.WorktreeInspection {
	wt := &conductor.WorktreeInspection{}

	info, err := os.Stat(worktreePath)
	if err != nil || !info.IsDir() {
		return wt
	}
	wt.Exists = true

	wt.Registered = isWorktreeRegistered(rootDir, worktreePath)

	if dirty, err := isWorktreeDirty(worktreePath); err == nil {
		wt.IsDirty = dirty
	}

	if head, err := worktreeHead(worktreePath); err == nil {
		wt.BranchHead = head
		if baseHead != "" && head != baseHead {
			wt.HasNewCommits = true
		}
	}

	return wt
}

func isWorktreeRegistered(rootDir, worktreePath string) bool {
	out, err := exec.Command("git", "-C", rootDir, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return false
	}
	want := canonicalFSPath(worktreePath)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			path := strings.TrimPrefix(line, "worktree ")
			// git canonicalizes symlinks in the paths it records (e.g. macOS
			// /var → /private/var) while PlanState.WorktreePath does not, so an
			// exact string compare yields false negatives. Canonicalize both.
			if canonicalFSPath(path) == want {
				return true
			}
		}
	}
	return false
}

// canonicalFSPath resolves a path to an absolute, symlink-free form for
// comparison. Falls back to the cleaned absolute path when the target doesn't
// resolve (e.g. already deleted), so a removed worktree compares unequal.
func canonicalFSPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	// The leaf no longer exists (e.g. the worktree dir was already removed), so
	// EvalSymlinks fails. Resolve the nearest existing ancestor's symlinks and
	// rejoin the missing tail, so a macOS /var → /private/var alias still
	// compares equal to the path git recorded while the dir existed.
	dir, rest := filepath.Dir(abs), filepath.Base(abs)
	for dir != filepath.Dir(dir) {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Clean(filepath.Join(resolved, rest))
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = filepath.Dir(dir)
	}
	return filepath.Clean(abs)
}

func isWorktreeDirty(worktreePath string) (bool, error) {
	cmd := exec.Command("git", "-C", worktreePath, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

func worktreeHead(worktreePath string) (string, error) {
	cmd := exec.Command("git", "-C", worktreePath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
