package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
)

func TestSpringfieldStartRunsAllPlansInQueue(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlansBinary(t, dir, []registeredPlan{
		{ID: "alpha", Title: "Implement alpha", Order: 1},
		{ID: "beta", Title: "Implement beta", Order: 2},
	})

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	fakeBinDir := filepath.Join(dir, "bin")
	installBranchAwareAgent(t, fakeBinDir, "claude")

	out1, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, out1)
	}
	if !strings.Contains(out1, "Plan: alpha") {
		t.Fatalf("expected alpha:\n%s", out1)
	}
	if !strings.Contains(out1, "Plan: beta") {
		t.Fatalf("expected beta:\n%s", out1)
	}
	if !strings.Contains(out1, "Queue completed") {
		t.Fatalf("expected queue completed:\n%s", out1)
	}

	state := readPlanStateFile(t, dir)
	if state.Plans["alpha"].Attempts != 1 {
		t.Fatalf("alpha attempts = %d, want 1", state.Plans["alpha"].Attempts)
	}
	if state.Plans["beta"].Attempts != 1 {
		t.Fatalf("beta attempts = %d, want 1", state.Plans["beta"].Attempts)
	}
	if state.Plans["alpha"].Status != "completed" || state.Plans["beta"].Status != "completed" {
		t.Fatalf("unexpected statuses: %+v", state.Plans)
	}

	// Second start: all plans already integrated → queue completed immediately
	out2, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err != nil {
		t.Fatalf("second start: %v\n%s", err, out2)
	}
	if !strings.Contains(out2, "Queue completed") {
		t.Fatalf("expected queue completed on restart:\n%s", out2)
	}
}

func TestSpringfieldStartResumesInterruptedPlanFromRecordedWorktree(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlansBinary(t, dir, []registeredPlan{
		{ID: "alpha", Title: "Implement alpha", Order: 1},
	})

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	wt := filepath.Join(dir, ".worktrees", "alpha")
	gitMust(t, dir, "worktree", "add", "-b", "springfield/alpha", wt, "main")
	baseHead := strings.TrimSpace(gitOut(t, dir, "rev-parse", "main"))
	writeConductorStateBinary(t, dir, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status:       conductor.StatusInterrupted,
				Attempts:     1,
				WorktreePath: wt,
				Branch:       "springfield/alpha",
				BaseRef:      "main",
				BaseHead:     baseHead,
				ExitReason:   conductor.ExitInterruptedProcessExit,
			},
		},
	})

	fakeBinDir := filepath.Join(dir, "bin")
	installBranchAwareAgent(t, fakeBinDir, "claude")

	out, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err != nil {
		t.Fatalf("resume start: %v\n%s", err, out)
	}
	if !strings.Contains(out, "reusing worktree") {
		t.Fatalf("expected worktree reuse on resume:\n%s", out)
	}
	if !strings.Contains(out, "Plan: alpha") {
		t.Fatalf("expected alpha run:\n%s", out)
	}

	state := readPlanStateFile(t, dir)
	if state.Plans["alpha"].Attempts != 2 {
		t.Fatalf("alpha attempts = %d, want 2", state.Plans["alpha"].Attempts)
	}
	if state.Plans["alpha"].Status != "completed" {
		t.Fatalf("alpha status = %q, want completed", state.Plans["alpha"].Status)
	}
}

func TestSpringfieldStartBatchRuntimeWinsOverConductorState(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlansBinary(t, dir, []registeredPlan{
		{ID: "alpha", Title: "Implement alpha", Order: 1},
	})
	writeConductorStateBinary(t, dir, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status:       conductor.StatusRunning,
				Attempts:     1,
				WorktreePath: filepath.Join(dir, ".worktrees", "alpha"),
				Branch:       "springfield/alpha",
				BaseRef:      "main",
				BaseHead:     "aaaaaaaa",
			},
		},
	})
	writeActiveBatchBinary(t, dir, "batch-001", "Active Batch")

	fakeBinDir := filepath.Join(dir, "bin")
	argvPath := filepath.Join(dir, "claude.argv")
	installFakeAgentBinary(t, fakeBinDir, "claude", argvPath)

	out, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir}, "start")
	if err != nil {
		t.Fatalf("start with active batch: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Batch: batch-001") {
		t.Fatalf("expected batch surface:\n%s", out)
	}
	if strings.Contains(out, "Plan: alpha") {
		t.Fatalf("conductor plan surfaced despite active batch:\n%s", out)
	}
	// Startup header must show the plan-centric rollup, not stale "Phase: 1 of N".
	if !strings.Contains(out, "Plans: ") || !strings.Contains(out, "integrated") {
		t.Fatalf("startup header missing plan-centric rollup; expected 'Plans: X/Y integrated':\n%s", out)
	}
	if strings.Contains(out, "Phase: 1 of") {
		t.Fatalf("stale 'Phase: 1 of N' resurrected in start header:\n%s", out)
	}

	state := readPlanStateFile(t, dir)
	if state.Plans["alpha"].Status != "running" {
		t.Fatalf("conductor state mutated despite batch precedence: %+v", state.Plans["alpha"])
	}
}

// TestSpringfieldStartHeaderDegradesWhenStateLoadFails mirrors
// TestStatusRollupDegradesWhenStateLoadFails for the start.go startup header.
// Corrupt state.json must produce a warn line on stderr and a header without
// the rollup, NOT an aborted batch (the later LoadProject call will error out
// for its own reasons — we only assert the header degraded gracefully).
func TestSpringfieldStartHeaderDegradesWhenStateLoadFails(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlansBinary(t, dir, []registeredPlan{
		{ID: "alpha", Title: "Implement alpha", Order: 1},
	})
	writeActiveBatchBinary(t, dir, "batch-001", "Active Batch")

	// Corrupt state.json after registry write so the startup-header
	// LoadProjectRaw call fails. The later LoadProject for runBatch will fail
	// too — that's fine; we only inspect the header output.
	corrupt := filepath.Join(dir, ".springfield", "execution", "state.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("corrupt state.json: %v", err)
	}

	fakeBinDir := filepath.Join(dir, "bin")
	argvPath := filepath.Join(dir, "claude.argv")
	installFakeAgentBinary(t, fakeBinDir, "claude", argvPath)

	// Start will fail later (LoadProject can't decode), but the header must
	// have printed first with a warn line and no rollup.
	out, _ := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir}, "start")
	if !strings.Contains(out, "Batch: batch-001") {
		t.Fatalf("expected batch header to render even when state load fails:\n%s", out)
	}
	if !strings.Contains(out, "[warn] could not load project state") {
		t.Fatalf("expected stderr warn line in combined output:\n%s", out)
	}
	if strings.Contains(out, "Plans: ") && strings.Contains(out, "integrated") {
		t.Fatalf("rollup line leaked when state load failed:\n%s", out)
	}
}

type registeredPlan struct {
	ID    string
	Title string
	Order int
}

func writeRegisteredPlansBinary(t *testing.T, root string, plans []registeredPlan) {
	t.Helper()

	units := make([]conductor.PlanUnit, 0, len(plans))
	for _, plan := range plans {
		writePlanFileBinary(t, root, ".springfield/plans", plan.ID, "# "+plan.Title+"\n\nDo the thing.\n")
		units = append(units, conductor.PlanUnit{
			ID:    plan.ID,
			Title: plan.Title,
			Path:  ".springfield/plans/" + plan.ID + ".md",
			Order: plan.Order,
		})
	}
	writeConductorConfigBinary(t, root, &conductor.Config{
		PlansDir:     ".springfield/plans",
		WorktreeBase: ".worktrees",
		MaxRetries:   1,
		Tool:         "claude",
		PlanUnits:    units,
	})
}

func writeConductorStateBinary(t *testing.T, root string, state *conductor.State) {
	t.Helper()

	dir := filepath.Join(root, ".springfield", "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir execution: %v", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

func writeActiveBatchBinary(t *testing.T, root, batchID, title string) {
	t.Helper()

	paths, err := batch.NewPaths(root, batchID)
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}
	b := batch.Batch{
		ID:      batchID,
		Title:   title,
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"01"}}},
		PlanIDs: []string{"01"},
	}
	if err := batch.WriteBatch(paths, b, "source", nil); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: batchID}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
}

func installBranchAwareAgent(t *testing.T, binDir, name string) {
	t.Helper()

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	const positiveSignalLine = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_fake"},{"type":"tool_result","tool_use_id":"toolu_fake","is_error":false}]}}`
	script := "#!/bin/sh\nset -e\n" +
		"git config user.email agent@example.com\n" +
		"git config user.name Agent\n" +
		"branch=$(git branch --show-current)\n" +
		"file=$(printf '%s' \"$branch\" | tr '/' '_').txt\n" +
		"printf '%s\\n' \"$branch\" > \"$file\"\n" +
		"git add \"$file\"\n" +
		"git commit -m \"$branch commit\" >/dev/null\n" +
		"echo '" + positiveSignalLine + "'\n"
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
}

func readPlanStateFile(t *testing.T, root string) struct {
	Plans map[string]struct {
		Status   string `json:"status"`
		Attempts int    `json:"attempts"`
	} `json:"plans"`
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
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("parse state: %v\n%s", err, data)
	}
	return state
}
