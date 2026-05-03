package cmd_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSpringfieldStartRunsRegisteredPlanInIsolatedWorktree is the parity-2
// black-box: a real git repo, a registered plan unit, a fake claude agent
// that asserts $PWD points at the worktree (not the source checkout), and
// truthful state recorded to .springfield/execution/state.json.
func TestSpringfieldStartRunsRegisteredPlanInIsolatedWorktree(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlan(t, dir, "alpha", "Implement alpha")

	// Stage + commit so the source is clean. Springfield's preflight
	// refuses dirty checkouts on a first run.
	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	fakeBinDir := filepath.Join(dir, "bin")
	pwdPath := filepath.Join(dir, "claude.pwd")
	installPwdRecordingAgent(t, fakeBinDir, "claude", pwdPath)

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err != nil {
		t.Fatalf("springfield start: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Status: completed") {
		t.Fatalf("expected completion, got:\n%s", output)
	}
	if !strings.Contains(output, "Worktree: ") {
		t.Fatalf("expected worktree line, got:\n%s", output)
	}

	// Agent's CWD must be the worktree, not the source root.
	pwdData, err := os.ReadFile(pwdPath)
	if err != nil {
		t.Fatalf("read agent pwd: %v", err)
	}
	pwd := strings.TrimSpace(string(pwdData))
	wantWtPrefix := filepath.Join(dir, ".worktrees", "alpha")
	resolvedWanted, _ := filepath.EvalSymlinks(wantWtPrefix)
	resolvedPwd, _ := filepath.EvalSymlinks(pwd)
	if resolvedPwd != resolvedWanted && resolvedPwd != wantWtPrefix && pwd != wantWtPrefix {
		t.Fatalf("agent CWD = %q, expected worktree %q", pwd, wantWtPrefix)
	}

	// Verify state recorded truthfully.
	stateBytes, err := os.ReadFile(filepath.Join(dir, ".springfield", "execution", "state.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var state struct {
		Plans map[string]struct {
			Status       string `json:"status"`
			WorktreePath string `json:"worktree_path"`
			Branch       string `json:"branch"`
			BaseRef      string `json:"base_ref"`
			BaseHead     string `json:"base_head"`
			InputDigest  string `json:"input_digest"`
			ExitReason   string `json:"exit_reason"`
			EvidencePath string `json:"evidence_path"`
			Agent        string `json:"agent"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatalf("parse state: %v\n%s", err, stateBytes)
	}
	got, ok := state.Plans["alpha"]
	if !ok {
		t.Fatalf("no state for alpha:\n%s", stateBytes)
	}
	if got.Status != "completed" {
		t.Fatalf("status = %q", got.Status)
	}
	if got.Branch != "springfield/alpha" {
		t.Fatalf("branch = %q", got.Branch)
	}
	if got.BaseRef == "" || got.BaseHead == "" || got.InputDigest == "" {
		t.Fatalf("missing identity fields: %+v", got)
	}
	if got.ExitReason != "completed" {
		t.Fatalf("exit reason = %q", got.ExitReason)
	}
	if got.EvidencePath == "" {
		t.Fatalf("missing evidence path")
	}
	if !strings.Contains(got.WorktreePath, ".worktrees") {
		t.Fatalf("worktree path drift: %q", got.WorktreePath)
	}
}

// TestSpringfieldStartRefusesDirtySource verifies the preflight matrix at
// the CLI surface: a dirty source checkout is refused on first run with a
// stable structured tag visible in state and exit message.
func TestSpringfieldStartRefusesDirtySource(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlan(t, dir, "alpha", "Implement alpha")

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	// Make source dirty.
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatalf("write dirty: %v", err)
	}

	fakeBinDir := filepath.Join(dir, "bin")
	installPwdRecordingAgent(t, fakeBinDir, "claude", filepath.Join(dir, "claude.pwd"))

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected dirty rejection, got success:\n%s", output)
	}
	if !strings.Contains(output, "preflight-dirty-source") {
		t.Fatalf("expected preflight-dirty-source tag, got:\n%s", output)
	}

	// State must record the tag for later diagnosis.
	stateBytes, err := os.ReadFile(filepath.Join(dir, ".springfield", "execution", "state.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !strings.Contains(string(stateBytes), "preflight-dirty-source") {
		t.Fatalf("state missing preflight tag:\n%s", stateBytes)
	}
}

// --- helpers ---

func initRealGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitMust(t, dir, "init", "--initial-branch=main")
	gitMust(t, dir, "config", "user.email", "test@example.com")
	gitMust(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	// Springfield runtime state (and the log it tees on start) lives under
	// .springfield/ — that directory must be gitignored so the dirty-source
	// preflight does not fire on its own bookkeeping.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"),
		[]byte(".springfield/\n.worktrees/\nbin/\nclaude.pwd\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	return dir
}

func gitMust(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// writeRegisteredPlan creates the plan body on disk and a config.json that
// registers it as the sole plan unit.
func writeRegisteredPlan(t *testing.T, root, id, title string) {
	t.Helper()
	planDir := filepath.Join(root, "springfield", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}
	if err := os.WriteFile(filepath.Join(planDir, id+".md"),
		[]byte("# "+title+"\n\nDo the thing.\n"), 0o644); err != nil {
		t.Fatalf("write plan body: %v", err)
	}
	cfg := map[string]any{
		"plans_dir":     "springfield/plans",
		"worktree_base": ".worktrees",
		"max_retries":   1,
		"tool":          "claude",
		"plan_units": []map[string]any{
			{"id": id, "title": title, "path": "springfield/plans/" + id + ".md", "order": 1},
		},
	}
	cfgPath := filepath.Join(root, ".springfield", "execution", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
}

// installPwdRecordingAgent installs a fake claude that records its working
// directory on first invocation so the test can assert the agent ran inside
// the worktree, not the source checkout. Emits the same positive-signal
// stream-json line installFakeAgentBinary uses so ValidateResult treats the
// run as a success.
func installPwdRecordingAgent(t *testing.T, binDir, name, pwdPath string) {
	t.Helper()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	const positiveSignalLine = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_fake"},{"type":"tool_result","tool_use_id":"toolu_fake","is_error":false}]}}`
	script := "#!/bin/sh\npwd > " + pwdPath + "\necho '" + positiveSignalLine + "'\necho 'agent-output'\n"
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
}
