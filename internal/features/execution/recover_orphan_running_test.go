package execution_test

import (
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/core/lock"
	"springfield/internal/features/conductor"
	"springfield/internal/features/execution"
)

// seedRunningPlan registers one plan and persists it in StatusRunning so the
// liveness probe has a stale marker to act on.
func seedRunningPlan(t *testing.T, root, id string) {
	t.Helper()
	writePlanFile(t, root, id+".md")
	if _, err := execution.AddPlan(root, execution.PlanInput{ID: id, Path: id + ".md"}); err != nil {
		t.Fatalf("add plan: %v", err)
	}
	writeState(t, root, map[string]any{
		"plans": map[string]any{
			id: map[string]any{
				"status":        "running",
				"attempts":      1,
				"worktree_path": filepath.Join(root, ".worktrees", id),
			},
		},
	})
}

// writeStaleLock plants a lock file whose recorded pid owns no live process,
// so lock.Inspect (flock probe) reports no live holder.
func writeStaleLock(t *testing.T, root string) {
	t.Helper()
	lockPath := filepath.Join(root, ".springfield", ".lock")
	if err := os.WriteFile(lockPath, []byte("000000999999\n2026-05-04T00:00:00Z\n"), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}
}

func TestResolveActiveBatchLivenessClearsProcessDeadRunning(t *testing.T) {
	root := newProject(t)
	seedRunningPlan(t, root, "alpha")
	writeStaleLock(t, root)

	live, err := execution.ResolveActiveBatchLiveness(root, true)
	if err != nil {
		t.Fatalf("ResolveActiveBatchLiveness: %v", err)
	}
	if live.Holder != nil {
		t.Fatalf("expected no live holder for a process-dead batch, got %+v", live.Holder)
	}
	if len(live.Cleared) != 1 || live.Cleared[0] != "alpha" {
		t.Fatalf("Cleared = %v, want [alpha]", live.Cleared)
	}

	// The clear must be persisted: a reload shows interrupted, not running.
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := project.State.Plans["alpha"].Status; got != conductor.StatusInterrupted {
		t.Fatalf("persisted status = %q, want interrupted", got)
	}
}

func TestResolveActiveBatchLivenessLeavesLiveBatchUntouched(t *testing.T) {
	root := newProject(t)
	seedRunningPlan(t, root, "alpha")

	lk, err := lock.Acquire(root)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer lk.Release()

	live, err := execution.ResolveActiveBatchLiveness(root, true)
	if err != nil {
		t.Fatalf("ResolveActiveBatchLiveness: %v", err)
	}
	if live.Holder == nil {
		t.Fatalf("expected a live holder while the lock is held")
	}
	if len(live.Cleared) != 0 {
		t.Fatalf("a live batch must not be cleared, got %v", live.Cleared)
	}

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := project.State.Plans["alpha"].Status; got != conductor.StatusRunning {
		t.Fatalf("live plan status = %q, want running (untouched)", got)
	}
}

func TestResolveActiveBatchLivenessDiagnoseDoesNotMutate(t *testing.T) {
	root := newProject(t)
	seedRunningPlan(t, root, "alpha")
	writeStaleLock(t, root)

	live, err := execution.ResolveActiveBatchLiveness(root, false)
	if err != nil {
		t.Fatalf("ResolveActiveBatchLiveness: %v", err)
	}
	if live.Holder != nil {
		t.Fatalf("expected no live holder, got %+v", live.Holder)
	}
	if len(live.StaleRunning) != 1 || live.StaleRunning[0] != "alpha" {
		t.Fatalf("StaleRunning = %v, want [alpha]", live.StaleRunning)
	}
	if len(live.Cleared) != 0 {
		t.Fatalf("diagnose must not clear, got %v", live.Cleared)
	}

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := project.State.Plans["alpha"].Status; got != conductor.StatusRunning {
		t.Fatalf("diagnose mutated status to %q, want running", got)
	}
}
