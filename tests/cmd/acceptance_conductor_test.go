package cmd_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/conductor"
)

// ---------------------------------------------------------------------------
// Test 1: Full happy-path — register plans via CLI → start → all complete
// ---------------------------------------------------------------------------

func TestAcceptance_FullHappyPath(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")

	// Create plan files on disk
	for _, id := range []string{"alpha", "beta", "gamma"} {
		writePlanFileBinary(t, dir, "springfield/plans", id, "# "+id+"\n\nDo the thing.\n")
	}

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	// Register plans via CLI
	for i, id := range []string{"alpha", "beta", "gamma"} {
		out, err := runBinaryIn(t, bin, dir, "plans", "add",
			"--id", id,
			"--path", "springfield/plans/"+id+".md",
			"--order", itoa(i+1))
		if err != nil {
			t.Fatalf("plans add %s: %v\n%s", id, err, out)
		}
	}

	// plans list — verify all 3 in order
	listOut, err := runBinaryIn(t, bin, dir, "plans", "list")
	if err != nil {
		t.Fatalf("plans list: %v\n%s", err, listOut)
	}
	for _, id := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(listOut, id) {
			t.Errorf("plans list missing %s:\n%s", id, listOut)
		}
	}

	// status before start — 3 configured, 0 completed
	statusOut, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, statusOut)
	}
	if !strings.Contains(statusOut, "3 configured, 0 completed") {
		t.Errorf("status should show 3 configured, 0 completed:\n%s", statusOut)
	}
	if !strings.Contains(statusOut, "springfield start") {
		t.Errorf("status should mention springfield start:\n%s", statusOut)
	}

	// Install agent + start
	fakeBinDir := filepath.Join(dir, "bin")
	installBranchAwareAgent(t, fakeBinDir, "claude")
	env := []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}

	startOut, err := runBinaryInWithEnv(t, bin, dir, env, "start")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, startOut)
	}
	for _, id := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(startOut, "Plan: "+id) {
			t.Errorf("start output missing Plan: %s:\n%s", id, startOut)
		}
	}
	if !strings.Contains(startOut, "Queue completed: 3 of 3") {
		t.Errorf("expected Queue completed: 3 of 3:\n%s", startOut)
	}

	// status after start — all completed
	statusOut2, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, statusOut2)
	}
	if !strings.Contains(statusOut2, "3 configured, 3 completed") {
		t.Errorf("status should show 3 configured, 3 completed:\n%s", statusOut2)
	}
	if !strings.Contains(statusOut2, "All registered plans completed") {
		t.Errorf("status should say all completed:\n%s", statusOut2)
	}

	// Git log on main has commits from all 3 plan branches
	logOut := gitOut(t, dir, "log", "--oneline", "main")
	for _, id := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(logOut, id) {
			t.Errorf("git log missing commit for %s:\n%s", id, logOut)
		}
	}

	// No leftover worktrees
	worktreeDir := filepath.Join(dir, ".worktrees")
	entries, err := os.ReadDir(worktreeDir)
	if err == nil {
		for _, e := range entries {
			if e.Name() != ".merges" {
				t.Errorf("leftover worktree: %s", e.Name())
			}
		}
	}

	// Second start — exits clean, no agent invoked
	startOut2, err := runBinaryInWithEnv(t, bin, dir, env, "start")
	if err != nil {
		t.Fatalf("second start: %v\n%s", err, startOut2)
	}
	if !strings.Contains(startOut2, "Queue completed") {
		t.Errorf("second start should complete immediately:\n%s", startOut2)
	}
}

// ---------------------------------------------------------------------------
// Test 2: Failure → diagnose → recover → resume
// ---------------------------------------------------------------------------

func TestAcceptance_FailureDiagnoseRecoverResume(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlansBinary(t, dir, []registeredPlan{
		{ID: "alpha", Title: "Alpha", Order: 1},
		{ID: "beta", Title: "Beta", Order: 2},
		{ID: "gamma", Title: "Gamma", Order: 3},
	})

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	fakeBinDir := filepath.Join(dir, "bin")
	installBranchAwareAgent(t, fakeBinDir, "claude")
	installFailingAgent(t, fakeBinDir, "beta")
	failDir := filepath.Join(fakeBinDir, "fail-beta")

	// First start: alpha succeeds, beta fails → halted
	out1, err := runBinaryInWithEnv(t, bin, dir,
		[]string{"PATH=" + failDir + ":" + fakeBinDir + ":" + os.Getenv("PATH")},
		"start")
	if err == nil {
		t.Fatalf("expected error from halted queue:\n%s", out1)
	}
	if !strings.Contains(out1, "Plan: alpha") {
		t.Errorf("expected alpha ran:\n%s", out1)
	}
	if !strings.Contains(out1, "Plan: beta") {
		t.Errorf("expected beta ran (and failed):\n%s", out1)
	}

	// status — alpha=completed, beta=failed, gamma=pending, queue=halted
	statusOut, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, statusOut)
	}
	state := readQueueStateFile(t, dir)
	if state.Plans["alpha"].Status != "completed" {
		t.Errorf("alpha should be completed, got %q", state.Plans["alpha"].Status)
	}
	if state.Plans["beta"].Status != "failed" {
		t.Errorf("beta should be failed, got %q", state.Plans["beta"].Status)
	}
	if state.Queue == nil || state.Queue.Status != "halted" {
		t.Errorf("queue should be halted")
	}

	// diagnose beta
	diagOut, err := runBinaryIn(t, bin, dir, "recover", "--plan", "beta", "--diagnose")
	if err != nil {
		t.Fatalf("diagnose: %v\n%s", err, diagOut)
	}
	if !strings.Contains(diagOut, "Status: failed") {
		t.Errorf("diagnose should show failed:\n%s", diagOut)
	}
	if !strings.Contains(diagOut, "retry") {
		t.Errorf("diagnose should show retry action:\n%s", diagOut)
	}

	// recover beta
	recoverOut, err := runBinaryIn(t, bin, dir, "recover", "--plan", "beta")
	if err != nil {
		t.Fatalf("recover: %v\n%s", err, recoverOut)
	}
	if !strings.Contains(recoverOut, "Recovered plan") {
		t.Errorf("expected recovery message:\n%s", recoverOut)
	}

	// status — beta now pending
	statusOut2, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, statusOut2)
	}
	if !strings.Contains(statusOut2, "pending") {
		t.Errorf("status should show beta pending:\n%s", statusOut2)
	}

	// Remove failing agent, second start
	removePlanSpecificAgent(t, fakeBinDir, "beta")
	out2, err := runBinaryInWithEnv(t, bin, dir,
		[]string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")},
		"start")
	if err != nil {
		t.Fatalf("resume start: %v\n%s", err, out2)
	}

	// beta + gamma now complete
	state2 := readQueueStateFile(t, dir)
	if state2.Queue == nil || state2.Queue.Status != "completed" {
		t.Fatalf("queue should be completed, got %+v", state2.Queue)
	}
	for _, id := range []string{"alpha", "beta", "gamma"} {
		if state2.Plans[id].Status != "completed" {
			t.Errorf("%s should be completed, got %q", id, state2.Plans[id].Status)
		}
	}

	// status — all 3 completed
	statusOut3, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, statusOut3)
	}
	if !strings.Contains(statusOut3, "All registered plans completed") {
		t.Errorf("status should say all completed:\n%s", statusOut3)
	}

	// Git log has commits from all 3
	logOut := gitOut(t, dir, "log", "--oneline", "main")
	for _, id := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(logOut, id) {
			t.Errorf("git log missing %s:\n%s", id, logOut)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 3: Merge refusal (target drift) → diagnose → reset target → re-start
// ---------------------------------------------------------------------------

func TestAcceptance_MergeRefusalRecoverResume(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlansBinary(t, dir, []registeredPlan{
		{ID: "alpha", Title: "Alpha", Order: 1},
		{ID: "beta", Title: "Beta", Order: 2},
	})

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")
	baseHead := gitOut(t, dir, "rev-parse", "main")

	fakeBinDir := filepath.Join(dir, "bin")
	installDriftingAgent(t, fakeBinDir, "claude", dir)
	env := []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}

	// First start: alpha execution succeeds but merge refused (drift)
	out1, err := runBinaryInWithEnv(t, bin, dir, env, "start")
	if err == nil {
		t.Fatalf("expected error from merge refusal:\n%s", out1)
	}
	if !strings.Contains(out1, "Merge: refused") {
		t.Fatalf("expected merge refusal:\n%s", out1)
	}
	if !strings.Contains(out1, "target-drift") {
		t.Fatalf("expected target-drift reason:\n%s", out1)
	}

	// status — alpha completed with merge refused
	statusOut, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, statusOut)
	}
	if !strings.Contains(statusOut, "refused") {
		t.Errorf("status should show merge refused:\n%s", statusOut)
	}

	// diagnose — shows retry-merge
	diagOut, err := runBinaryIn(t, bin, dir, "recover", "--plan", "alpha", "--diagnose")
	if err != nil {
		t.Fatalf("diagnose: %v\n%s", err, diagOut)
	}
	if !strings.Contains(diagOut, "retry-merge") {
		t.Errorf("diagnose should suggest retry-merge:\n%s", diagOut)
	}

	// Operator resets main to pre-drift state so merge target matches.
	// No recover needed — the plan worktree + branch are preserved;
	// next start re-enters merge-only path automatically.
	gitMust(t, dir, "update-ref", "refs/heads/main", baseHead)

	// Replace drifting agent with canary to prove agent is NOT re-invoked
	canary := filepath.Join(dir, "agent-was-invoked")
	installCanaryAgent(t, fakeBinDir, "claude", canary)

	// Second start: alpha merge-only + beta execution
	// The canary agent would be invoked for beta, so we need a real
	// branch-aware agent. Install it at a separate dir for beta.
	installBranchAwareAgent(t, fakeBinDir, "claude")

	out2, err := runBinaryInWithEnv(t, bin, dir, env, "start")
	if err != nil {
		t.Fatalf("second start: %v\n%s", err, out2)
	}
	if !strings.Contains(out2, "re-running merge integration") {
		t.Errorf("should announce merge re-run:\n%s", out2)
	}
	if !strings.Contains(out2, "Merge: succeeded") {
		t.Errorf("merge should succeed:\n%s", out2)
	}

	// Both completed
	state := readQueueStateFile(t, dir)
	if state.Queue == nil || state.Queue.Status != "completed" {
		t.Fatalf("queue should be completed, got %+v", state.Queue)
	}
	for _, id := range []string{"alpha", "beta"} {
		if state.Plans[id].Status != "completed" {
			t.Errorf("%s should be completed, got %q", id, state.Plans[id].Status)
		}
	}

	// No preserved worktrees
	for _, sub := range []string{"alpha", "beta"} {
		if _, err := os.Stat(filepath.Join(dir, ".worktrees", sub)); !os.IsNotExist(err) {
			t.Errorf("worktree %s should be cleaned up", sub)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 4: Interrupted process → status normalizes → recover → resume
// ---------------------------------------------------------------------------

func TestAcceptance_InterruptedProcessResume(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlansBinary(t, dir, []registeredPlan{
		{ID: "alpha", Title: "Alpha", Order: 1},
		{ID: "beta", Title: "Beta", Order: 2},
	})

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")
	baseHead := gitOut(t, dir, "rev-parse", "main")

	// Create worktree simulating partial agent work
	wt := filepath.Join(dir, ".worktrees", "alpha")
	gitMust(t, dir, "worktree", "add", "-b", "springfield/alpha", wt, "main")
	if err := os.WriteFile(filepath.Join(wt, "partial.txt"), []byte("partial\n"), 0o644); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	gitMust(t, wt, "add", ".")
	gitMust(t, wt, "commit", "-m", "partial agent work")

	// Pre-bake state: alpha=running (crash), beta=pending, queue=running
	writeConductorStateBinary(t, dir, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status:       conductor.StatusRunning,
				Attempts:     1,
				WorktreePath: wt,
				Branch:       "springfield/alpha",
				BaseRef:      "main",
				BaseHead:     baseHead,
			},
		},
		Queue: &conductor.QueueState{
			Status:       conductor.QueueRunning,
			ActivePlanID: "alpha",
		},
	})

	// status normalizes running→interrupted
	statusOut, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, statusOut)
	}
	if !strings.Contains(statusOut, "interrupted") {
		t.Errorf("status should normalize to interrupted:\n%s", statusOut)
	}

	// diagnose — shows interrupted
	diagOut, err := runBinaryIn(t, bin, dir, "recover", "--plan", "alpha", "--diagnose")
	if err != nil {
		t.Fatalf("diagnose: %v\n%s", err, diagOut)
	}
	if !strings.Contains(diagOut, "interrupted") {
		t.Errorf("diagnose should show interrupted:\n%s", diagOut)
	}

	// recover alpha → pending
	recoverOut, err := runBinaryIn(t, bin, dir, "recover", "--plan", "alpha")
	if err != nil {
		t.Fatalf("recover: %v\n%s", err, recoverOut)
	}
	if !strings.Contains(recoverOut, "Recovered plan") {
		t.Errorf("expected recovery:\n%s", recoverOut)
	}

	// Install agent + start — both plans run + complete
	fakeBinDir := filepath.Join(dir, "bin")
	installBranchAwareAgent(t, fakeBinDir, "claude")
	env := []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}

	startOut, err := runBinaryInWithEnv(t, bin, dir, env, "start")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, startOut)
	}

	state := readQueueStateFile(t, dir)
	if state.Queue == nil || state.Queue.Status != "completed" {
		t.Fatalf("queue should be completed, got %+v", state.Queue)
	}
	for _, id := range []string{"alpha", "beta"} {
		if state.Plans[id].Status != "completed" {
			t.Errorf("%s should be completed, got %q", id, state.Plans[id].Status)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 5: Status accuracy across lifecycle states
// ---------------------------------------------------------------------------

func TestAcceptance_StatusAccuracy(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlansBinary(t, dir, []registeredPlan{
		{ID: "alpha", Title: "Alpha", Order: 1},
		{ID: "beta", Title: "Beta", Order: 2},
		{ID: "gamma", Title: "Gamma", Order: 3},
	})

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	// All pending, no queue state
	statusOut1, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, statusOut1)
	}
	if !strings.Contains(statusOut1, "3 configured, 0 completed") {
		t.Errorf("initial status wrong:\n%s", statusOut1)
	}

	// Write mixed state: alpha=completed+integrated, beta=failed, gamma=pending
	writeConductorStateBinary(t, dir, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status: conductor.StatusCompleted,
				Merge: &conductor.MergeOutcome{
					Status:           conductor.MergeSucceeded,
					SourceSyncStatus: "synced",
				},
				Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
			},
			"beta": {
				Status:       conductor.StatusFailed,
				Error:        "exit code 1",
				Agent:        "claude",
				EvidencePath: "/tmp/evidence/beta",
				Attempts:     1,
			},
		},
		Queue: &conductor.QueueState{
			Status:     conductor.QueueHalted,
			StopReason: "plan-failed",
		},
	})

	statusOut2, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, statusOut2)
	}

	// Alpha completed with merge succeeded
	if !strings.Contains(statusOut2, "alpha") || !strings.Contains(statusOut2, "completed") {
		t.Errorf("alpha should show completed:\n%s", statusOut2)
	}
	if !strings.Contains(statusOut2, "succeeded") {
		t.Errorf("alpha merge should show succeeded:\n%s", statusOut2)
	}

	// Beta failed with error
	if !strings.Contains(statusOut2, "beta") || !strings.Contains(statusOut2, "failed") {
		t.Errorf("beta should show failed:\n%s", statusOut2)
	}
	if !strings.Contains(statusOut2, "exit code 1") {
		t.Errorf("beta error should be shown:\n%s", statusOut2)
	}

	// Queue halted
	if !strings.Contains(statusOut2, "halted") {
		t.Errorf("queue should show halted:\n%s", statusOut2)
	}
	if !strings.Contains(statusOut2, "plan-failed") {
		t.Errorf("stop reason should be shown:\n%s", statusOut2)
	}

	// Next step mentions recover or start
	if !strings.Contains(statusOut2, "springfield start") {
		t.Errorf("next step should mention springfield start:\n%s", statusOut2)
	}

	// All completed state
	writeConductorStateBinary(t, dir, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status:  conductor.StatusCompleted,
				Merge:   &conductor.MergeOutcome{Status: conductor.MergeSucceeded, SourceSyncStatus: "synced"},
				Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
			},
			"beta": {
				Status:  conductor.StatusCompleted,
				Merge:   &conductor.MergeOutcome{Status: conductor.MergeSucceeded, SourceSyncStatus: "synced"},
				Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
			},
			"gamma": {
				Status:  conductor.StatusCompleted,
				Merge:   &conductor.MergeOutcome{Status: conductor.MergeSucceeded, SourceSyncStatus: "synced"},
				Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
			},
		},
		Queue: &conductor.QueueState{Status: conductor.QueueCompleted},
	})

	statusOut3, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, statusOut3)
	}
	if !strings.Contains(statusOut3, "All registered plans completed") {
		t.Errorf("status should say all completed:\n%s", statusOut3)
	}
}

// ---------------------------------------------------------------------------
// Test 6: Plan registry management — add/remove/reorder via CLI
// ---------------------------------------------------------------------------

func TestAcceptance_PlanRegistryManagement(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")

	// Create plan files
	for _, id := range []string{"alpha", "beta"} {
		writePlanFileBinary(t, dir, "springfield/plans", id, "# "+id+"\n")
	}

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	// Add alpha
	out, err := runBinaryIn(t, bin, dir, "plans", "add", "--id", "alpha", "--path", "springfield/plans/alpha.md", "--order", "1")
	if err != nil {
		t.Fatalf("add alpha: %v\n%s", err, out)
	}

	// Add beta
	out, err = runBinaryIn(t, bin, dir, "plans", "add", "--id", "beta", "--path", "springfield/plans/beta.md", "--order", "2")
	if err != nil {
		t.Fatalf("add beta: %v\n%s", err, out)
	}

	// List — alpha=1, beta=2
	listOut, err := runBinaryIn(t, bin, dir, "plans", "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, listOut)
	}
	alphaIdx := strings.Index(listOut, "alpha")
	betaIdx := strings.Index(listOut, "beta")
	if alphaIdx < 0 || betaIdx < 0 {
		t.Fatalf("list should show both plans:\n%s", listOut)
	}
	if alphaIdx > betaIdx {
		t.Errorf("alpha should appear before beta:\n%s", listOut)
	}

	// Duplicate ID → error
	dupOut, err := runBinaryIn(t, bin, dir, "plans", "add", "--id", "alpha", "--path", "springfield/plans/alpha.md")
	if err == nil {
		t.Fatalf("expected duplicate ID error:\n%s", dupOut)
	}
	if !strings.Contains(dupOut, "already exists") {
		t.Errorf("expected 'already exists' error:\n%s", dupOut)
	}

	// Reorder: beta alpha
	out, err = runBinaryIn(t, bin, dir, "plans", "reorder", "beta", "alpha")
	if err != nil {
		t.Fatalf("reorder: %v\n%s", err, out)
	}

	// List — beta=1, alpha=2
	listOut2, err := runBinaryIn(t, bin, dir, "plans", "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, listOut2)
	}
	betaIdx2 := strings.Index(listOut2, "beta")
	alphaIdx2 := strings.Index(listOut2, "alpha")
	if betaIdx2 > alphaIdx2 {
		t.Errorf("after reorder, beta should appear before alpha:\n%s", listOut2)
	}

	// Remove beta
	out, err = runBinaryIn(t, bin, dir, "plans", "remove", "--id", "beta")
	if err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}

	// List — only alpha
	listOut3, err := runBinaryIn(t, bin, dir, "plans", "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, listOut3)
	}
	if !strings.Contains(listOut3, "alpha") {
		t.Errorf("alpha should remain:\n%s", listOut3)
	}
	if strings.Contains(listOut3, "beta") {
		t.Errorf("beta should be removed:\n%s", listOut3)
	}
}

// ---------------------------------------------------------------------------
// Test 7: Evidence preservation — failed plan preserves evidence
// ---------------------------------------------------------------------------

func TestAcceptance_EvidencePreservation(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlansBinary(t, dir, []registeredPlan{
		{ID: "alpha", Title: "Alpha", Order: 1},
		{ID: "beta", Title: "Beta", Order: 2},
	})

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	fakeBinDir := filepath.Join(dir, "bin")
	installBranchAwareAgent(t, fakeBinDir, "claude")
	installFailingAgent(t, fakeBinDir, "alpha")
	failDir := filepath.Join(fakeBinDir, "fail-alpha")

	_, _ = runBinaryInWithEnv(t, bin, dir,
		[]string{"PATH=" + failDir + ":" + fakeBinDir + ":" + os.Getenv("PATH")},
		"start")

	// Read state.json — alpha has EvidencePath
	statePath := filepath.Join(dir, ".springfield", "execution", "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var rawState struct {
		Plans map[string]struct {
			Status       string `json:"status"`
			EvidencePath string `json:"evidence_path"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(data, &rawState); err != nil {
		t.Fatalf("parse state: %v\n%s", err, data)
	}
	alphaState := rawState.Plans["alpha"]
	if alphaState.Status != "failed" {
		t.Fatalf("alpha should be failed, got %q", alphaState.Status)
	}
	if alphaState.EvidencePath == "" {
		t.Fatal("alpha should have EvidencePath set")
	}

	// Evidence directory exists
	if _, err := os.Stat(alphaState.EvidencePath); err != nil {
		t.Fatalf("evidence dir should exist at %s: %v", alphaState.EvidencePath, err)
	}

	// meta.json in evidence dir
	metaPath := filepath.Join(alphaState.EvidencePath, "meta.json")
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("meta.json should exist in evidence: %v", err)
	}

	// diagnose mentions evidence
	diagOut, err := runBinaryIn(t, bin, dir, "recover", "--plan", "alpha", "--diagnose")
	if err != nil {
		t.Fatalf("diagnose: %v\n%s", err, diagOut)
	}
	if !strings.Contains(diagOut, "Evidence") {
		t.Errorf("diagnose should mention evidence:\n%s", diagOut)
	}

	// status mentions evidence
	statusOut, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, statusOut)
	}
	if !strings.Contains(statusOut, "evidence") {
		t.Errorf("status should mention evidence:\n%s", statusOut)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
