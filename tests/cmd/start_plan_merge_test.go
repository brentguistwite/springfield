package cmd_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSpringfieldStartMergesPlanBranchOnSuccess is the parity-3 happy-path
// black-box: a real git repo, a registered plan, an agent that creates a
// commit on the plan branch in its worktree. After execution, Springfield
// merges the plan branch into the recorded target through a dedicated
// merge worktree, advances the target ref via fast-forward, and cleans up
// the merge worktree, execution worktree, and plan branch.
func TestSpringfieldStartMergesPlanBranchOnSuccess(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlan(t, dir, "alpha", "Implement alpha")

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	beforeMain := gitOut(t, dir, "rev-parse", "main")

	fakeBinDir := filepath.Join(dir, "bin")
	installCommittingAgent(t, fakeBinDir, "claude", "feature.txt", "agent commit")

	output, err := runBinaryInWithEnv(t, bin, dir,
		[]string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")},
		"start")
	if err != nil {
		t.Fatalf("springfield start: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Merge: succeeded") {
		t.Fatalf("expected merge success line, got:\n%s", output)
	}
	if !strings.Contains(output, "Cleanup: succeeded") {
		t.Fatalf("expected clean cleanup, got:\n%s", output)
	}

	// Target ref must have advanced to the plan branch's commit.
	afterMain := gitOut(t, dir, "rev-parse", "main")
	if afterMain == beforeMain {
		t.Fatalf("main did not advance: before=%s after=%s\n%s", beforeMain, afterMain, output)
	}
	logOut := gitOut(t, dir, "log", "--format=%s", "main", "-n", "1")
	if !strings.Contains(logOut, "agent commit") {
		t.Fatalf("agent commit not reachable on main; got: %q", logOut)
	}
	// H1: target was the source checkout's HEAD branch, so Springfield
	// must have synced the source worktree to the post-merge head.
	// Anything outside Springfield-owned paths (.springfield/, .worktrees/)
	// would mean the next IsDirty preflight refuses a fresh start with a
	// phantom "uncommitted changes" rejection.
	if dirt := nonSpringfieldStatus(t, dir); dirt != "" {
		t.Fatalf("source checkout should be clean after merge sync; got:\n%s", dirt)
	}
	// Agent's committed file must be present in the source worktree.
	if _, err := os.Stat(filepath.Join(dir, "feature.txt")); err != nil {
		t.Fatalf("expected synced feature.txt in source worktree; stat err=%v", err)
	}

	// Plan branch and worktrees should be gone.
	branches := gitOut(t, dir, "branch", "--list", "springfield/alpha")
	if strings.TrimSpace(branches) != "" {
		t.Fatalf("plan branch still present: %q", branches)
	}
	if _, err := os.Stat(filepath.Join(dir, ".worktrees", "alpha")); !os.IsNotExist(err) {
		t.Fatalf("execution worktree should be deleted, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".worktrees", ".merges", "alpha")); !os.IsNotExist(err) {
		t.Fatalf("merge worktree should be deleted, stat err=%v", err)
	}

	// State must record the merge succeeded with refs/SHAs.
	stateBytes, err := os.ReadFile(filepath.Join(dir, ".springfield", "execution", "state.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var state struct {
		Plans map[string]struct {
			Status   string `json:"status"`
			PlanHead string `json:"plan_head"`
			Merge    struct {
				Status           string `json:"status"`
				Mode             string `json:"mode"`
				TargetRef        string `json:"target_ref"`
				TargetHead       string `json:"target_head"`
				PostMergeHead    string `json:"post_merge_head"`
				SourceSyncStatus string `json:"source_sync_status"`
			} `json:"merge"`
			Cleanup struct {
				Status string `json:"status"`
			} `json:"cleanup"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatalf("parse state: %v\n%s", err, stateBytes)
	}
	got := state.Plans["alpha"]
	if got.Status != "completed" {
		t.Fatalf("execution status: %q", got.Status)
	}
	if got.Merge.Status != "succeeded" || got.Merge.Mode != "ff-only" {
		t.Fatalf("merge state: %+v", got.Merge)
	}
	if got.Merge.TargetRef != "main" || got.Merge.TargetHead == "" || got.Merge.PostMergeHead == "" {
		t.Fatalf("merge refs/SHAs missing: %+v", got.Merge)
	}
	if got.PlanHead == "" {
		t.Fatalf("plan_head not recorded")
	}
	if got.Cleanup.Status != "succeeded" {
		t.Fatalf("cleanup status: %q", got.Cleanup.Status)
	}
	if got.Merge.SourceSyncStatus != "synced" {
		t.Fatalf("merge.source_sync_status = %q, want synced", got.Merge.SourceSyncStatus)
	}
}

// TestSpringfieldStartRefusesMergeOnTargetDrift is the parity-3 strict
// policy black-box: between when Springfield records base_head and when the
// merge phase tries to publish, the target branch advances (simulated by
// the agent committing on main from inside the source checkout). The merge
// must be refused; both worktrees and the plan branch must remain on disk;
// state must record the refusal.
func TestSpringfieldStartRefusesMergeOnTargetDrift(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlan(t, dir, "alpha", "Implement alpha")

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	fakeBinDir := filepath.Join(dir, "bin")
	// Drift agent: makes a plan-branch commit AND advances main in the
	// source checkout before exiting, so the merge phase observes a moved
	// target.
	installDriftingAgent(t, fakeBinDir, "claude", dir)

	output, err := runBinaryInWithEnv(t, bin, dir,
		[]string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")},
		"start")
	if err == nil {
		t.Fatalf("expected merge refusal to surface as non-zero exit, got success:\n%s", output)
	}
	if !strings.Contains(output, "Merge: refused") {
		t.Fatalf("expected merge refusal line, got:\n%s", output)
	}
	if !strings.Contains(output, "target-drift") {
		t.Fatalf("expected target-drift reason, got:\n%s", output)
	}

	// Plan branch and execution worktree must be preserved.
	branches := gitOut(t, dir, "branch", "--list", "springfield/alpha")
	if strings.TrimSpace(branches) == "" {
		t.Fatalf("plan branch was deleted on refusal")
	}
	if _, err := os.Stat(filepath.Join(dir, ".worktrees", "alpha")); err != nil {
		t.Fatalf("execution worktree must be preserved, stat err=%v", err)
	}
	// Merge worktree must NOT have been created.
	if _, err := os.Stat(filepath.Join(dir, ".worktrees", ".merges", "alpha")); !os.IsNotExist(err) {
		t.Fatalf("merge worktree must not exist on pre-create refusal: stat err=%v", err)
	}

	stateBytes, err := os.ReadFile(filepath.Join(dir, ".springfield", "execution", "state.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var state struct {
		Plans map[string]struct {
			Status string `json:"status"`
			Merge  struct {
				Status string `json:"status"`
				Reason string `json:"reason"`
			} `json:"merge"`
			Cleanup struct {
				Status            string `json:"status"`
				ExecutionWorktree struct {
					Status string `json:"status"`
				} `json:"execution_worktree"`
				PlanBranch struct {
					Status string `json:"status"`
				} `json:"plan_branch"`
			} `json:"cleanup"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatalf("parse state: %v\n%s", err, stateBytes)
	}
	got := state.Plans["alpha"]
	if got.Status != "completed" {
		t.Fatalf("execution status overwritten: %q", got.Status)
	}
	if got.Merge.Status != "refused" || got.Merge.Reason != "target-drift" {
		t.Fatalf("merge state: %+v", got.Merge)
	}
	if got.Cleanup.ExecutionWorktree.Status != "preserved" {
		t.Fatalf("execution worktree cleanup: %q", got.Cleanup.ExecutionWorktree.Status)
	}
	if got.Cleanup.PlanBranch.Status != "preserved" {
		t.Fatalf("plan branch cleanup: %q", got.Cleanup.PlanBranch.Status)
	}
}

// TestSpringfieldStartFailsWhenSourceResyncIsBlockedByUserEdits proves the
// CLI exit-code contract for source-sync failures. The agent makes a
// commit on the plan branch; before the merge phase runs, the user
// edits a tracked file in the source checkout (simulated by an agent
// that touches a tracked file in the source root before exiting).
// `springfield start` must:
//   - publish the merge (target ref advances)
//   - record SourceSyncStatus="failed" with a descriptive error
//   - exit non-zero with truthful output naming source resync failure
//   - leave the source worktree as the user left it (no silent reset)
func TestSpringfieldStartFailsWhenSourceResyncIsBlockedByUserEdits(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlan(t, dir, "alpha", "Implement alpha")

	if err := os.WriteFile(filepath.Join(dir, "user.txt"), []byte("original\n"), 0o644); err != nil {
		t.Fatalf("write user.txt: %v", err)
	}
	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")
	beforeMain := gitOut(t, dir, "rev-parse", "main")

	fakeBinDir := filepath.Join(dir, "bin")
	installEditingAgent(t, fakeBinDir, "claude", dir)

	output, err := runBinaryInWithEnv(t, bin, dir,
		[]string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")},
		"start")
	if err == nil {
		t.Fatalf("expected non-zero exit when source resync blocked; got success:\n%s", output)
	}
	if !strings.Contains(output, "source resync failed") {
		t.Fatalf("expected source resync failure in output:\n%s", output)
	}

	afterMain := gitOut(t, dir, "rev-parse", "main")
	if afterMain == beforeMain {
		t.Fatalf("main should have advanced even when resync was blocked: before=%s after=%s", beforeMain, afterMain)
	}

	userBytes, err := os.ReadFile(filepath.Join(dir, "user.txt"))
	if err != nil {
		t.Fatalf("read user.txt: %v", err)
	}
	if !strings.Contains(string(userBytes), "user edit") {
		t.Fatalf("user edit was overwritten by reset; user.txt = %q", userBytes)
	}

	stateBytes, err := os.ReadFile(filepath.Join(dir, ".springfield", "execution", "state.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !strings.Contains(string(stateBytes), `"source_sync_status": "failed"`) {
		t.Fatalf("state should record source_sync_status=failed:\n%s", stateBytes)
	}
}

// installEditingAgent installs a fake claude that commits on the plan
// branch AND modifies a tracked file in the source root before exiting,
// so the merge phase observes user dirt against the recorded base_head
// when it reaches the source-resync gate.
func installEditingAgent(t *testing.T, binDir, name, sourceRoot string) {
	t.Helper()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	const positiveSignalLine = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_fake"},{"type":"tool_result","tool_use_id":"toolu_fake","is_error":false}]}}`
	script := "#!/bin/sh\nset -e\n" +
		"git config user.email agent@example.com\n" +
		"git config user.name Agent\n" +
		"echo content > feature.txt\n" +
		"git add feature.txt\n" +
		"git commit -m 'plan commit' >/dev/null\n" +
		"echo 'user edit' > " + sourceRoot + "/user.txt\n" +
		"echo '" + positiveSignalLine + "'\n"
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write editing agent: %v", err)
	}
}

// TestSpringfieldStartReRunsMergeOnlyAfterDriftRefusal proves the slice-3
// re-run contract: after a target-drift refusal preserves the execution
// worktree and plan branch, a second `springfield start` must skip
// re-execution (no agent dispatch) and drive only the merge integration
// against the existing artifacts. Once the operator restores the target,
// the re-run merges cleanly and removes the preserved artifacts.
func TestSpringfieldStartReRunsMergeOnlyAfterDriftRefusal(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlan(t, dir, "alpha", "Implement alpha")

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")
	baseHead := gitOut(t, dir, "rev-parse", "main")

	fakeBinDir := filepath.Join(dir, "bin")
	// First run: drift agent commits on plan branch AND advances main —
	// merge phase will refuse with target-drift.
	installDriftingAgent(t, fakeBinDir, "claude", dir)
	out1, err := runBinaryInWithEnv(t, bin, dir,
		[]string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")},
		"start")
	if err == nil {
		t.Fatalf("expected first run to refuse merge; got success:\n%s", out1)
	}
	if !strings.Contains(out1, "Merge: refused") {
		t.Fatalf("first run missing merge refusal:\n%s", out1)
	}

	// Operator resets main back to recorded base_head so the merge target
	// matches. Plan branch and execution worktree are untouched.
	gitMust(t, dir, "update-ref", "refs/heads/main", baseHead)

	// Replace the agent with one that would COMMIT IN THE EXECUTION
	// WORKTREE if dispatched. The re-run must skip agent dispatch entirely;
	// if the agent is invoked, it would touch a file we can detect.
	canary := filepath.Join(dir, "agent-was-invoked")
	installCanaryAgent(t, fakeBinDir, "claude", canary)

	out2, err := runBinaryInWithEnv(t, bin, dir,
		[]string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")},
		"start")
	if err != nil {
		t.Fatalf("second run: %v\n%s", err, out2)
	}
	if !strings.Contains(out2, "re-running merge integration") {
		t.Fatalf("second run did not announce merge-only re-run:\n%s", out2)
	}
	if !strings.Contains(out2, "Merge: succeeded") {
		t.Fatalf("second run did not record merge success:\n%s", out2)
	}
	if _, err := os.Stat(canary); !os.IsNotExist(err) {
		t.Fatalf("agent must NOT be invoked on merge-only re-run; canary=%v", err)
	}

	// Final state: plan branch + execution worktree removed; main advanced.
	if branches := gitOut(t, dir, "branch", "--list", "springfield/alpha"); strings.TrimSpace(branches) != "" {
		t.Fatalf("plan branch should be gone, got %q", branches)
	}
	if _, err := os.Stat(filepath.Join(dir, ".worktrees", "alpha")); !os.IsNotExist(err) {
		t.Fatalf("execution worktree should be deleted, stat err=%v", err)
	}
	stateBytes, _ := os.ReadFile(filepath.Join(dir, ".springfield", "execution", "state.json"))
	if !strings.Contains(string(stateBytes), `"status": "succeeded"`) {
		t.Fatalf("state should record merge success on re-entry:\n%s", stateBytes)
	}
	// Execution status must remain "completed", never rewritten to "failed".
	if !strings.Contains(string(stateBytes), `"status": "completed"`) {
		t.Fatalf("execution status was clobbered:\n%s", stateBytes)
	}
}

// installCanaryAgent installs a fake claude that touches a canary file if
// invoked. Used to assert the merge-only re-run path does NOT dispatch the
// agent.
func installCanaryAgent(t *testing.T, binDir, name, canary string) {
	t.Helper()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	const positiveSignalLine = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_fake"},{"type":"tool_result","tool_use_id":"toolu_fake","is_error":false}]}}`
	script := "#!/bin/sh\ntouch " + canary + "\necho '" + positiveSignalLine + "'\n"
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write canary agent: %v", err)
	}
}

// nonSpringfieldStatus returns the lines of `git status --porcelain` that
// are NOT under Springfield-owned prefixes (.springfield/, .worktrees/).
// Mirrors the slice-2 IsDirty filter so the test asserts the same notion
// of "clean source" the preflight uses.
func nonSpringfieldStatus(t *testing.T, dir string) string {
	t.Helper()
	out := gitOut(t, dir, "status", "--porcelain")
	if out == "" {
		return ""
	}
	var dirty []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		path := line[3:]
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = path[idx+len(" -> "):]
		}
		path = strings.Trim(path, "\"")
		if strings.HasPrefix(path, ".springfield/") || strings.HasPrefix(path, ".worktrees/") {
			continue
		}
		dirty = append(dirty, line)
	}
	return strings.Join(dirty, "\n")
}

// gitOut returns trimmed stdout of a git command run in dir.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// installCommittingAgent installs a fake claude that creates a file in its
// CWD (the plan worktree) and commits it on the current branch, then emits
// the positive-signal stream-json line so the runtime treats the run as
// successful.
func installCommittingAgent(t *testing.T, binDir, name, file, msg string) {
	t.Helper()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	const positiveSignalLine = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_fake"},{"type":"tool_result","tool_use_id":"toolu_fake","is_error":false}]}}`
	script := "#!/bin/sh\nset -e\n" +
		"git config user.email agent@example.com\n" +
		"git config user.name Agent\n" +
		"echo content > " + file + "\n" +
		"git add " + file + "\n" +
		"git commit -m '" + msg + "' >/dev/null\n" +
		"echo '" + positiveSignalLine + "'\n"
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
}

// installDriftingAgent installs a fake claude that commits on its own plan
// branch AND advances the source checkout's main ref before exiting, so the
// merge phase sees a moved target.
func installDriftingAgent(t *testing.T, binDir, name, sourceRoot string) {
	t.Helper()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	const positiveSignalLine = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_fake"},{"type":"tool_result","tool_use_id":"toolu_fake","is_error":false}]}}`
	script := "#!/bin/sh\nset -e\n" +
		"git config user.email agent@example.com\n" +
		"git config user.name Agent\n" +
		"echo content > feature.txt\n" +
		"git add feature.txt\n" +
		"git commit -m 'plan commit' >/dev/null\n" +
		// Advance main in the source checkout to simulate concurrent drift.
		"git -C " + sourceRoot + " -c user.email=drift@example.com -c user.name=Drift commit --allow-empty -m 'drift' >/dev/null\n" +
		"echo '" + positiveSignalLine + "'\n"
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
}
