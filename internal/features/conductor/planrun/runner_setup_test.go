package planrun_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/config"
	coreexec "springfield/internal/core/exec"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planrun"
	"springfield/internal/features/worktreesetup"
)

// setupEvidenceDir returns the on-disk setup evidence directory for a plan.
func setupEvidenceDir(root, planKey string) string {
	return filepath.Join(planrun.EvidenceRoot(root, planKey), "setup")
}

// TestSetupFailureBlocksAgentDispatch is the load-bearing AC: a non-zero setup
// exit fails the slice BEFORE any agent runs, with an error naming the command
// and exit code. No agent dispatch may happen.
func TestSetupFailureBlocksAgentDispatch(t *testing.T) {
	root := projectFixtureWithUnpassedStory(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()
	runner := &fakeAgentRunner{}
	runner.events = []coreexec.Event{
		{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>"},
	}

	var setupCalls int
	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
		SetupConfig:  config.SetupConfig{Enabled: true, Command: "make setup"},
		SetupCommand: func(_ context.Context, _ worktreesetup.Request) worktreesetup.Result {
			setupCalls++
			return worktreesetup.Result{ExitCode: 7}
		},
	})

	if setupCalls != 1 {
		t.Fatalf("setup ran %d times, want 1", setupCalls)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("agent dispatched %d times after failed setup, want 0", len(runner.calls))
	}
	if res.Reason != "setup-failed" {
		t.Fatalf("reason = %q, want setup-failed", res.Reason)
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "make setup") ||
		!strings.Contains(res.Err.Error(), "exit code 7") {
		t.Fatalf("error must name command and exit code, got: %v", res.Err)
	}

	// State on disk records the failure.
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	st := reloaded.State.Plans["alpha"]
	if st == nil || st.Status != conductor.StatusFailed || st.ExitReason != "setup-failed" {
		t.Fatalf("state = %+v, want failed/setup-failed", st)
	}
}

// TestSetupAbsentKeepsBehaviorIdentical proves the opt-in guarantee: with no
// [setup] block the slice runs exactly as before — the agent is dispatched and
// no setup evidence is written.
func TestSetupAbsentKeepsBehaviorIdentical(t *testing.T) {
	root := projectFixtureWithUnpassedStory(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()
	runner := &fakeAgentRunner{}
	runner.events = []coreexec.Event{
		{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>"},
	}
	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
		// No SetupConfig: zero value.
	})
	if res.Err != nil {
		t.Fatalf("SinglePlan: %v", res.Err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("agent dispatched %d times, want 1", len(runner.calls))
	}
	if _, err := os.Stat(setupEvidenceDir(root, res.Context.PlanKey)); !os.IsNotExist(err) {
		t.Fatalf("setup evidence dir exists but no [setup] configured: %v", err)
	}
}

// TestSetupRunsBeforeDispatchWithEnvAndEvidence exercises the real Run path: an
// enabled setup command executes in the worktree with the source-root/worktree
// env vars, succeeds, the agent is then dispatched, and setup output is captured
// under the evidence directory.
func TestSetupRunsBeforeDispatchWithEnvAndEvidence(t *testing.T) {
	root := projectFixtureWithUnpassedStory(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()
	runner := &fakeAgentRunner{}
	runner.events = []coreexec.Event{
		{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>"},
	}
	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
		SetupConfig: config.SetupConfig{
			Enabled: true,
			// Write both env vars into a marker file inside the worktree.
			Command: `echo "$SPRINGFIELD_SOURCE_ROOT|$SPRINGFIELD_WORKTREE" > setup-ran.txt; echo done 1>&2`,
		},
	})
	if res.Err != nil {
		t.Fatalf("SinglePlan: %v", res.Err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("agent dispatched %d times, want 1 (setup should have succeeded)", len(runner.calls))
	}

	// Setup actually ran in the worktree and saw the env vars.
	marker, err := os.ReadFile(filepath.Join(res.Context.WorktreeRoot, "setup-ran.txt"))
	if err != nil {
		t.Fatalf("setup marker not written in worktree: %v", err)
	}
	got := strings.TrimSpace(string(marker))
	wantPrefix := root + "|"
	if !strings.HasPrefix(got, wantPrefix) || !strings.HasSuffix(got, res.Context.WorktreeRoot) {
		t.Fatalf("env vars wrong: marker=%q want %s...%s", got, root, res.Context.WorktreeRoot)
	}

	// Evidence captured under <evidenceDir>/setup/.
	evDir := setupEvidenceDir(root, res.Context.PlanKey)
	metaBytes, err := os.ReadFile(filepath.Join(evDir, "setup.json"))
	if err != nil {
		t.Fatalf("read setup.json: %v", err)
	}
	var meta struct {
		ExitCode int `json:"exit_code"`
	}
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("unmarshal setup.json: %v", err)
	}
	if meta.ExitCode != 0 {
		t.Errorf("setup.json exit_code = %d, want 0", meta.ExitCode)
	}
	if _, err := os.Stat(filepath.Join(evDir, "stderr.txt")); err != nil {
		t.Errorf("stderr.txt missing: %v", err)
	}
}
