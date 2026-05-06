package conductor_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"springfield/internal/core/config"
	"springfield/internal/features/conductor"
)

func TestLoadProjectReadsConfigFromSpringfieldRuntime(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"01-bootstrap", "02-config", "03-runtime"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	if project.Config.Tool != "claude" {
		t.Fatalf("tool: got %q want claude", project.Config.Tool)
	}

	if got := project.AllPlans(); len(got) != 3 || got[0] != "01-bootstrap" || got[2] != "03-runtime" {
		t.Fatalf("all plans: got %v", got)
	}
}

func TestLoadProjectUsesRepoRootWhenStartedNested(t *testing.T) {
	root := t.TempDir()
	nested := root + "/plans/release"
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"01-bootstrap", "02-config", "03-runtime"})

	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	project, err := conductor.LoadProject(nested)
	if err != nil {
		t.Fatalf("load project from nested dir: %v", err)
	}

	if got := project.AllPlans()[0]; got != "01-bootstrap" {
		t.Fatalf("first plan: got %q want 01-bootstrap", got)
	}
}

func TestLoadProjectRequiresSpringfieldConfig(t *testing.T) {
	root := t.TempDir()

	_, err := conductor.LoadProject(root)
	if err == nil {
		t.Fatal("expected missing config error")
	}

	var missing *config.MissingConfigError
	if !errors.As(err, &missing) {
		t.Fatalf("expected MissingConfigError, got %T", err)
	}
}

func TestLoadProjectRequiresConductorConfig(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)

	_, err := conductor.LoadProject(root)
	if err == nil {
		t.Fatal("expected missing conductor config error")
	}
}

func TestSaveStateRoundTrips(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"01-bootstrap", "02-config", "03-runtime"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	project.MarkCompleted("01-bootstrap", "claude", "/tmp/evidence")
	project.MarkFailed("02-config", "timeout", "claude", "")
	if err := project.SaveState(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload project: %v", err)
	}

	if got := reloaded.PlanStatus("01-bootstrap"); got != conductor.StatusCompleted {
		t.Fatalf("01-bootstrap status: got %q want %q", got, conductor.StatusCompleted)
	}

	if got := reloaded.PlanError("02-config"); got != "timeout" {
		t.Fatalf("02-config error: got %q want timeout", got)
	}
}

func TestMarkRunningRecordsStartedAt(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"01-bootstrap", "02-config", "03-runtime"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	project.MarkRunning("01-bootstrap")

	ps := project.State.Plans["01-bootstrap"]
	if ps.StartedAt.IsZero() {
		t.Fatal("expected StartedAt to be set")
	}
}

func TestMarkCompletedRecordsEndedAtAndAgent(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"01-bootstrap", "02-config", "03-runtime"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	project.MarkRunning("01-bootstrap")
	project.MarkCompleted("01-bootstrap", "claude", "/tmp/evidence")

	ps := project.State.Plans["01-bootstrap"]
	if ps.EndedAt.IsZero() {
		t.Fatal("expected EndedAt to be set")
	}
	if ps.Agent != "claude" {
		t.Fatalf("agent: got %q want claude", ps.Agent)
	}
	if ps.EvidencePath != "/tmp/evidence" {
		t.Fatalf("evidence path: got %q want /tmp/evidence", ps.EvidencePath)
	}
	if !ps.EndedAt.After(ps.StartedAt) && !ps.EndedAt.Equal(ps.StartedAt) {
		t.Fatalf("EndedAt %v should be >= StartedAt %v", ps.EndedAt, ps.StartedAt)
	}
}

func TestMarkFailedRecordsEndedAtAgentAndEvidence(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"01-bootstrap", "02-config", "03-runtime"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	project.MarkRunning("01-bootstrap")
	project.MarkFailed("01-bootstrap", "timeout", "codex", "/tmp/evidence")

	ps := project.State.Plans["01-bootstrap"]
	if ps.EndedAt.IsZero() {
		t.Fatal("expected EndedAt to be set")
	}
	if ps.Agent != "codex" {
		t.Fatalf("agent: got %q want codex", ps.Agent)
	}
	if ps.EvidencePath != "/tmp/evidence" {
		t.Fatalf("evidence path: got %q want /tmp/evidence", ps.EvidencePath)
	}
	if ps.Error != "timeout" {
		t.Fatalf("error: got %q want timeout", ps.Error)
	}
}

func TestAttemptsIncrementOnRerun(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"01-bootstrap", "02-config", "03-runtime"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	project.MarkRunning("01-bootstrap")
	if got := project.State.Plans["01-bootstrap"].Attempts; got != 1 {
		t.Fatalf("attempts after first run: got %d want 1", got)
	}

	project.MarkFailed("01-bootstrap", "timeout", "claude", "")

	// Simulate resume
	project.MarkRunning("01-bootstrap")
	if got := project.State.Plans["01-bootstrap"].Attempts; got != 2 {
		t.Fatalf("attempts after resume: got %d want 2", got)
	}
}

func TestExpandedStateRoundTrips(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"01-bootstrap", "02-config", "03-runtime"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	project.MarkRunning("01-bootstrap")
	project.MarkCompleted("01-bootstrap", "claude", "/evidence/01")
	project.MarkRunning("02-config")
	project.MarkFailed("02-config", "exit 1", "codex", "/evidence/02")

	if err := project.SaveState(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload project: %v", err)
	}

	// Completed plan
	ps1 := reloaded.State.Plans["01-bootstrap"]
	if ps1.Agent != "claude" {
		t.Fatalf("01 agent: got %q want claude", ps1.Agent)
	}
	if ps1.EvidencePath != "/evidence/01" {
		t.Fatalf("01 evidence path: got %q want /evidence/01", ps1.EvidencePath)
	}
	if ps1.StartedAt.IsZero() {
		t.Fatal("01 StartedAt should persist")
	}
	if ps1.EndedAt.IsZero() {
		t.Fatal("01 EndedAt should persist")
	}
	if ps1.Attempts != 1 {
		t.Fatalf("01 attempts: got %d want 1", ps1.Attempts)
	}

	// Failed plan
	ps2 := reloaded.State.Plans["02-config"]
	if ps2.Agent != "codex" {
		t.Fatalf("02 agent: got %q want codex", ps2.Agent)
	}
	if ps2.EvidencePath != "/evidence/02" {
		t.Fatalf("02 evidence path: got %q want /evidence/02", ps2.EvidencePath)
	}
	if ps2.Error != "exit 1" {
		t.Fatalf("02 error: got %q want 'exit 1'", ps2.Error)
	}
}

func TestPlanAccessorsReturnExpandedFields(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"01-bootstrap", "02-config", "03-runtime"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	// Defaults for unknown plan
	if got := project.PlanAgent("unknown"); got != "" {
		t.Fatalf("default agent: got %q want empty", got)
	}
	if got := project.PlanEvidencePath("unknown"); got != "" {
		t.Fatalf("default evidence: got %q want empty", got)
	}
	if got := project.PlanAttempts("unknown"); got != 0 {
		t.Fatalf("default attempts: got %d want 0", got)
	}

	project.MarkRunning("01-bootstrap")
	project.MarkFailed("01-bootstrap", "err", "codex", "/ev/01")

	if got := project.PlanAgent("01-bootstrap"); got != "codex" {
		t.Fatalf("agent: got %q want codex", got)
	}
	if got := project.PlanEvidencePath("01-bootstrap"); got != "/ev/01" {
		t.Fatalf("evidence: got %q want /ev/01", got)
	}
	if got := project.PlanAttempts("01-bootstrap"); got != 1 {
		t.Fatalf("attempts: got %d want 1", got)
	}
}

func TestNormalizeStaleRunningMarksInterruptedAndPersists(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha", "beta"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	project.State.Plans["alpha"] = &conductor.PlanState{
		Status:       conductor.StatusRunning,
		Attempts:     1,
		StartedAt:    time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC),
		WorktreePath: filepath.Join(root, ".worktrees", "alpha"),
		Branch:       "springfield/alpha",
		BaseRef:      "main",
		BaseHead:     "aaaaaaaa",
	}
	if err := project.SaveState(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	at := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	changed := project.NormalizeStaleRunning(at)
	if len(changed) != 1 || changed[0] != "alpha" {
		t.Fatalf("changed = %v, want [alpha]", changed)
	}
	if err := project.SaveState(); err != nil {
		t.Fatalf("save normalized state: %v", err)
	}

	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload project: %v", err)
	}
	ps := reloaded.State.Plans["alpha"]
	if ps.Status != conductor.StatusInterrupted {
		t.Fatalf("status = %q, want %q", ps.Status, conductor.StatusInterrupted)
	}
	if ps.ExitReason != conductor.ExitInterruptedProcessExit {
		t.Fatalf("exit reason = %q, want %q", ps.ExitReason, conductor.ExitInterruptedProcessExit)
	}
	if !ps.EndedAt.Equal(at) {
		t.Fatalf("ended_at = %v, want %v", ps.EndedAt, at)
	}
	if ps.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", ps.Attempts)
	}

	next := conductor.BuildSchedule(reloaded.Config).NextPlans(reloaded.State)
	if len(next) != 1 || next[0] != "alpha" {
		t.Fatalf("next = %v, want [alpha]", next)
	}
}

func TestReloadedIntegratedPlanSkipsToNextIncompletePlan(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha", "beta"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge: &conductor.MergeOutcome{
			Status: conductor.MergeSucceeded,
		},
		Cleanup: &conductor.CleanupOutcome{
			Status: conductor.CleanupSucceeded,
		},
	}
	if err := project.SaveState(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload project: %v", err)
	}

	next := conductor.BuildSchedule(reloaded.Config).NextPlans(reloaded.State)
	if len(next) != 1 || next[0] != "beta" {
		t.Fatalf("next = %v, want [beta]", next)
	}
}
