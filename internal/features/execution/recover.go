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
}

// ResolveActiveBatchLiveness probes process liveness of the active batch's
// owner via the control-plane flock. When a live process holds the lock it
// returns the Holder and touches nothing. When no live process holds it
// (process-dead orphan), it lists the stale running plans; if clear is true it
// also normalizes them to interrupted and persists, returning them in Cleared.
// clear=false is the read-only diagnose path.
func ResolveActiveBatchLiveness(rootDir string, clear bool) (BatchLiveness, error) {
	if held := lock.Inspect(rootDir); held != nil {
		return BatchLiveness{Holder: held}, nil
	}

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
		return BatchLiveness{StaleRunning: stale}, nil
	}

	cleared := project.NormalizeStaleRunning(time.Now())
	if len(cleared) > 0 {
		if err := project.SaveState(); err != nil {
			return BatchLiveness{}, fmt.Errorf("persist stale-running normalization: %w", err)
		}
	}
	return BatchLiveness{StaleRunning: stale, Cleared: cleared}, nil
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
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			path := strings.TrimPrefix(line, "worktree ")
			if path == worktreePath {
				return true
			}
		}
	}
	return false
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
