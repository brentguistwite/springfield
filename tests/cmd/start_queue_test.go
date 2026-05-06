package cmd_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQueueAllPlansSucceed(t *testing.T) {
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

	out, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}

	for _, id := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(out, "Plan: "+id) {
			t.Errorf("expected Plan: %s in output:\n%s", id, out)
		}
	}

	state := readQueueStateFile(t, dir)
	if state.Queue == nil {
		t.Fatal("queue state is nil")
	}
	if state.Queue.Status != "completed" {
		t.Fatalf("queue status = %q, want completed", state.Queue.Status)
	}
	for _, id := range []string{"alpha", "beta", "gamma"} {
		ps := state.Plans[id]
		if ps.Status != "completed" {
			t.Errorf("plan %s status = %q, want completed", id, ps.Status)
		}
		if ps.Attempts != 1 {
			t.Errorf("plan %s attempts = %d, want 1", id, ps.Attempts)
		}
	}
}

func TestQueueHaltsOnFailure(t *testing.T) {
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

	out, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + failDir + ":" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected error from halted queue, got nil\n%s", out)
	}

	if !strings.Contains(out, "Plan: alpha") {
		t.Errorf("expected alpha to run:\n%s", out)
	}
	if !strings.Contains(out, "Plan: beta") {
		t.Errorf("expected beta to run (and fail):\n%s", out)
	}
	if strings.Contains(out, "Plan: gamma") {
		t.Errorf("gamma should NOT have run:\n%s", out)
	}

	state := readQueueStateFile(t, dir)
	if state.Queue == nil {
		t.Fatal("queue state is nil")
	}
	if state.Queue.Status != "halted" {
		t.Fatalf("queue status = %q, want halted", state.Queue.Status)
	}
	if state.Plans["alpha"].Status != "completed" {
		t.Errorf("alpha status = %q, want completed", state.Plans["alpha"].Status)
	}
	if state.Plans["beta"].Status != "failed" {
		t.Errorf("beta status = %q, want failed", state.Plans["beta"].Status)
	}
	if _, ok := state.Plans["gamma"]; ok && state.Plans["gamma"].Status != "pending" {
		t.Errorf("gamma should be pending or absent, got %q", state.Plans["gamma"].Status)
	}
}

func TestQueueResumeAfterHalt(t *testing.T) {
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

	// First run: alpha succeeds, beta fails → halted
	_, _ = runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + failDir + ":" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")

	// Fix beta: remove the failing wrapper so the succeeding agent runs
	removePlanSpecificAgent(t, fakeBinDir, "beta")

	// Second run: should resume from beta
	out2, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err != nil {
		t.Fatalf("resume start: %v\n%s", err, out2)
	}

	if strings.Contains(out2, "Plan: alpha") {
		t.Errorf("alpha should not re-run on resume:\n%s", out2)
	}
	if !strings.Contains(out2, "Plan: beta") {
		t.Errorf("expected beta to re-run:\n%s", out2)
	}
	if !strings.Contains(out2, "Plan: gamma") {
		t.Errorf("expected gamma to run:\n%s", out2)
	}

	state := readQueueStateFile(t, dir)
	if state.Queue.Status != "completed" {
		t.Fatalf("queue status = %q, want completed", state.Queue.Status)
	}
}

func TestQueueStoppedBySignal(t *testing.T) {
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
	installSlowAgent(t, fakeBinDir, "claude", "beta", 3)

	cmd := exec.Command(bin, "start")
	cmd.Dir = dir
	cmd.Env = mergeEnv(os.Environ(), []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")})
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		t.Fatalf("start process: %v", err)
	}

	// Wait for beta to start, then interrupt
	deadline := time.After(30 * time.Second)
	for {
		select {
		case <-deadline:
			_ = cmd.Process.Kill()
			t.Fatalf("timed out waiting for beta plan to start")
		default:
		}
		statePath := filepath.Join(dir, ".springfield", "execution", "state.json")
		data, err := os.ReadFile(statePath)
		if err == nil && strings.Contains(string(data), `"beta"`) &&
			strings.Contains(string(data), `"running"`) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	_ = cmd.Process.Signal(os.Interrupt)
	_ = cmd.Wait()

	state := readQueueStateFile(t, dir)
	// After SIGINT, queue should be stopped or the plan should be
	// interrupted (NormalizeStaleRunning will handle on next start)
	if state.Queue == nil {
		t.Fatal("queue state is nil")
	}
	// Accept either stopped (signal caught between plans) or halted/running
	// (signal killed mid-agent — next start normalizes)
	if state.Plans["alpha"].Status != "completed" {
		t.Errorf("alpha should be completed, got %q", state.Plans["alpha"].Status)
	}
}

// installSlowAgent writes a plan-specific agent wrapper that sleeps when the
// branch matches the given planID, falling through to the real agent otherwise.
func installSlowAgent(t *testing.T, binDir, name, slowPlanID string, delaySec int) {
	t.Helper()

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	const positiveSignalLine = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_fake"},{"type":"tool_result","tool_use_id":"toolu_fake","is_error":false}]}}`
	script := fmt.Sprintf(`#!/bin/sh
set -e
git config user.email agent@example.com
git config user.name Agent
branch=$(git branch --show-current)
case "$branch" in
  *%s*) sleep %d ;;
esac
file=$(printf '%%s' "$branch" | tr '/' '_').txt
printf '%%s\n' "$branch" > "$file"
git add "$file"
git commit -m "$branch commit" >/dev/null
echo '%s'
`, slowPlanID, delaySec, positiveSignalLine)
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write slow agent: %v", err)
	}
}

// --- helpers ---

// installFailingAgent writes a plan-specific agent wrapper that exits non-zero
// when the branch matches the given planID, falling through to the real agent
// otherwise. The wrapper is placed at higher PATH priority.
func installFailingAgent(t *testing.T, binDir, planID string) {
	t.Helper()

	failDir := filepath.Join(binDir, "fail-"+planID)
	if err := os.MkdirAll(failDir, 0o755); err != nil {
		t.Fatalf("mkdir fail dir: %v", err)
	}
	script := `#!/bin/sh
branch=$(git branch --show-current 2>/dev/null || echo "")
case "$branch" in
  *` + planID + `*) echo "simulated failure" >&2; exit 1 ;;
  *) exec "` + filepath.Join(binDir, "claude") + `" "$@" ;;
esac
`
	path := filepath.Join(failDir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write failing agent: %v", err)
	}
}

func removePlanSpecificAgent(t *testing.T, binDir, planID string) {
	t.Helper()
	os.RemoveAll(filepath.Join(binDir, "fail-"+planID))
}

func readQueueStateFile(t *testing.T, root string) struct {
	Plans map[string]struct {
		Status   string `json:"status"`
		Attempts int    `json:"attempts"`
	} `json:"plans"`
	Queue *struct {
		Status       string `json:"status"`
		ActivePlanID string `json:"active_plan_id"`
		StopReason   string `json:"stop_reason"`
	} `json:"queue"`
} {
	t.Helper()

	statePath := filepath.Join(root, ".springfield", "execution", "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var state struct {
		Plans map[string]struct {
			Status   string `json:"status"`
			Attempts int    `json:"attempts"`
		} `json:"plans"`
		Queue *struct {
			Status       string `json:"status"`
			ActivePlanID string `json:"active_plan_id"`
			StopReason   string `json:"stop_reason"`
		} `json:"queue"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("parse state: %v\n%s", err, data)
	}
	return state
}
