package execution

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"springfield/internal/features/conductor"
)

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
