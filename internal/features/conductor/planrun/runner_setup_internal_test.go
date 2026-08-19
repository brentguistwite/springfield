package planrun

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"springfield/internal/core/config"
	"springfield/internal/features/worktreesetup"
)

// TestRunWorktreeSetupGatesOnCompletionMarkerNotReuse pins the lifecycle fix:
// setup skips on a reuse resume ONLY when a prior run exited zero (proven by the
// completion marker), NOT merely because the worktree exists. A reuse whose
// setup never completed (crash between worktree creation and setup finishing)
// must re-run setup rather than dispatch an agent into a half-built tree.
func TestRunWorktreeSetupGatesOnCompletionMarkerNotReuse(t *testing.T) {
	root := t.TempDir()
	ctx := Context{PlanKey: "alpha", WorktreeRoot: filepath.Join(root, "wt")}
	evidenceDir := EvidenceRoot(root, ctx.PlanKey)

	var calls int
	in := SinglePlanInput{
		ControlRoot: root,
		SetupConfig: config.SetupConfig{Enabled: true, Command: "make setup"},
		SetupCommand: func(_ context.Context, _ worktreesetup.Request) worktreesetup.Result {
			calls++
			return worktreesetup.Result{ExitCode: 0}
		},
	}

	// 1) Fresh worktree (Reuse=false): setup runs and records the marker.
	if r := runWorktreeSetup(in, "alpha", ctx, PrepareDecision{Reuse: false}, nil, time.Now); r != nil {
		t.Fatalf("fresh setup returned failure: %+v", r)
	}
	if calls != 1 {
		t.Fatalf("fresh worktree: setup ran %d times, want 1", calls)
	}
	if !worktreesetup.IsComplete(evidenceDir, "make setup", nil) {
		t.Fatalf("successful setup did not write the completion marker")
	}

	// 2) Reuse WITH the marker present: setup is skipped (already completed).
	if r := runWorktreeSetup(in, "alpha", ctx, PrepareDecision{Reuse: true}, nil, time.Now); r != nil {
		t.Fatalf("reuse-with-marker returned failure: %+v", r)
	}
	if calls != 1 {
		t.Fatalf("reuse with completed marker: setup ran %d times, want it skipped (1)", calls)
	}

	// 3) Simulate a crash after worktree creation but before setup completed:
	// the marker is absent. A reuse resume MUST re-run setup.
	if err := worktreesetup.ClearComplete(evidenceDir); err != nil {
		t.Fatalf("ClearComplete: %v", err)
	}
	if r := runWorktreeSetup(in, "alpha", ctx, PrepareDecision{Reuse: true}, nil, time.Now); r != nil {
		t.Fatalf("reuse-after-crash returned failure: %+v", r)
	}
	if calls != 2 {
		t.Fatalf("reuse after crashed setup: setup ran %d times, want re-run (2)", calls)
	}
	if !worktreesetup.IsComplete(evidenceDir, "make setup", nil) {
		t.Fatalf("re-run setup did not re-record the completion marker")
	}
}

// TestRunWorktreeSetupReRunsOnChangedCommand pins the command-keyed marker fix:
// a reuse resume whose [setup] command changed between runs must NOT trust the
// prior command's completion marker — it re-runs setup with the new command
// rather than silently skipping it. The worktree itself is still reused (a
// changed setup command is not worktree drift); only the setup step re-fires.
func TestRunWorktreeSetupReRunsOnChangedCommand(t *testing.T) {
	root := t.TempDir()
	ctx := Context{PlanKey: "alpha", WorktreeRoot: filepath.Join(root, "wt")}
	evidenceDir := EvidenceRoot(root, ctx.PlanKey)

	var lastCmd string
	var calls int
	mk := func(cmd string) SinglePlanInput {
		return SinglePlanInput{
			ControlRoot: root,
			SetupConfig: config.SetupConfig{Enabled: true, Command: cmd},
			SetupCommand: func(_ context.Context, req worktreesetup.Request) worktreesetup.Result {
				calls++
				lastCmd = req.Command
				return worktreesetup.Result{ExitCode: 0}
			},
		}
	}

	// 1) Fresh run with the original command records a marker keyed on it.
	if r := runWorktreeSetup(mk("npm install"), "alpha", ctx, PrepareDecision{Reuse: false}, nil, time.Now); r != nil {
		t.Fatalf("fresh setup returned failure: %+v", r)
	}
	if calls != 1 {
		t.Fatalf("fresh worktree: setup ran %d times, want 1", calls)
	}

	// 2) Reuse with the SAME command: skipped.
	if r := runWorktreeSetup(mk("npm install"), "alpha", ctx, PrepareDecision{Reuse: true}, nil, time.Now); r != nil {
		t.Fatalf("reuse-same-command returned failure: %+v", r)
	}
	if calls != 1 {
		t.Fatalf("reuse with unchanged command: setup ran %d times, want it skipped (1)", calls)
	}

	// 3) Reuse with a CHANGED command: the old marker must not be trusted → re-run.
	if r := runWorktreeSetup(mk("npm ci"), "alpha", ctx, PrepareDecision{Reuse: true}, nil, time.Now); r != nil {
		t.Fatalf("reuse-changed-command returned failure: %+v", r)
	}
	if calls != 2 {
		t.Fatalf("reuse with changed command: setup ran %d times, want re-run (2)", calls)
	}
	if lastCmd != "npm ci" {
		t.Fatalf("re-run dispatched command %q, want the changed command", lastCmd)
	}
	if !worktreesetup.IsComplete(evidenceDir, "npm ci", nil) {
		t.Fatalf("re-run did not re-key the completion marker to the new command")
	}
}
