package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/conductor"
	"springfield/internal/features/prd"
)

// registerPRDPlanUnit writes a config.json registering planID as a single
// prd.json plan unit and writes the prd.json with the given stories. Unlike
// writeRegisteredPlan (which registers a legacy .md path), this is the shape
// --mark-completed needs: a prd.json whose per-story passes state is validated.
func registerPRDPlanUnit(t *testing.T, root, planID, title string, stories []prd.UserStory) {
	t.Helper()
	prdDir := filepath.Join(root, ".springfield", "plans", planID)
	if err := os.MkdirAll(prdDir, 0o755); err != nil {
		t.Fatalf("mkdir prd dir: %v", err)
	}
	doc := prd.PRD{ID: planID, Title: title, UserStories: stories}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal prd: %v", err)
	}
	if err := os.WriteFile(filepath.Join(prdDir, "prd.json"), data, 0o644); err != nil {
		t.Fatalf("write prd.json: %v", err)
	}

	cfg := map[string]any{
		"plans_dir":     ".springfield/plans",
		"worktree_base": ".worktrees",
		"max_retries":   1,
		"tool":          "claude",
		"plan_units": []map[string]any{
			{"id": planID, "title": title, "path": ".springfield/plans/" + planID + "/prd.json", "order": 1},
		},
	}
	cfgPath := filepath.Join(root, ".springfield", "execution", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	cfgData, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(cfgPath, cfgData, 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
}

// TestRecoverPlanMarkCompletedRejectsUnpassedStories proves the validation
// gate at the CLI surface: a plan with an unpassed story cannot be marked
// completed, the error names the offender, and state stays failed.
func TestRecoverPlanMarkCompletedRejectsUnpassedStories(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	writeSpringfieldConfig(t, root, "claude")
	registerPRDPlanUnit(t, root, "alpha", "Alpha", []prd.UserStory{
		{ID: "US-001", Passes: true},
		{ID: "US-002", Passes: false},
	})

	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {Status: conductor.StatusFailed, Error: "exit code 1"},
		},
	})

	output, err := runBinaryIn(t, bin, root, "recover", "--plan", "alpha", "--mark-completed")
	if err == nil {
		t.Fatalf("expected rejection for unpassed stories, got success:\n%s", output)
	}
	if !strings.Contains(output, "US-002") {
		t.Errorf("error should name unpassed story US-002:\n%s", output)
	}

	// State must remain failed.
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	if project.State.Plans["alpha"].Status != conductor.StatusFailed {
		t.Fatalf("status mutated on rejection: %q", project.State.Plans["alpha"].Status)
	}
}

// TestRecoverPlanMarkCompletedHelpDocumented proves the flag is surfaced in
// --help, satisfying the discoverability acceptance criterion.
func TestRecoverPlanMarkCompletedHelpDocumented(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	output, err := runBinaryIn(t, bin, root, "recover", "--help")
	if err != nil {
		t.Fatalf("recover --help: %v\n%s", err, output)
	}
	if !strings.Contains(output, "--mark-completed") {
		t.Errorf("--help should document --mark-completed:\n%s", output)
	}
}

// TestRecoverPlanMarkCompletedFullFlow proves the full A9 recovery: a plan
// Springfield recorded as failed (but whose work is done on the plan branch
// and whose stories all pass) is marked completed, which queues a pending
// merge; the subsequent `springfield start` performs the merge and advances
// the target ref.
func TestRecoverPlanMarkCompletedFullFlow(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	registerPRDPlanUnit(t, dir, "alpha", "Alpha", []prd.UserStory{
		{ID: "US-001", Passes: true},
		{ID: "US-002", Passes: true},
	})

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")
	baseHead := gitOut(t, dir, "rev-parse", "main")

	// Build a plan branch + execution worktree with a commit ahead of main,
	// mimicking a finished-but-not-integrated agent run.
	gitMust(t, dir, "branch", "springfield/alpha", "main")
	wtPath := filepath.Join(dir, ".worktrees", "alpha")
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		t.Fatalf("mkdir worktrees: %v", err)
	}
	gitMust(t, dir, "worktree", "add", wtPath, "springfield/alpha")
	if err := os.WriteFile(filepath.Join(wtPath, "feature.txt"), []byte("agent work\n"), 0o644); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	gitMust(t, wtPath, "add", ".")
	gitMust(t, wtPath, "commit", "-m", "agent commit")
	planHead := gitOut(t, wtPath, "rev-parse", "HEAD")

	// Springfield recorded a failure even though the work landed.
	writeConductorStateBinary(t, dir, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status:       conductor.StatusFailed,
				Error:        "agent timeout after 3600s",
				Agent:        "claude",
				Attempts:     1,
				WorktreePath: wtPath,
				Branch:       "springfield/alpha",
				BaseRef:      "main",
				BaseHead:     baseHead,
				PlanHead:     planHead,
			},
		},
	})

	// Step 1: mark completed.
	out, err := runBinaryIn(t, bin, dir, "recover", "--plan", "alpha", "--mark-completed")
	if err != nil {
		t.Fatalf("recover --mark-completed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Marked plan") {
		t.Errorf("expected mark-completed confirmation:\n%s", out)
	}
	if !strings.Contains(out, "springfield start") {
		t.Errorf("expected next-step guidance:\n%s", out)
	}

	// State must now be completed + merge pending, error cleared.
	project, err := conductor.LoadProject(dir)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	ps := project.State.Plans["alpha"]
	if ps.Status != conductor.StatusCompleted {
		t.Fatalf("status = %q, want completed", ps.Status)
	}
	if ps.Error != "" {
		t.Errorf("error should be cleared, got %q", ps.Error)
	}
	if ps.Merge == nil || ps.Merge.Status != conductor.MergePending {
		t.Fatalf("merge = %+v, want pending", ps.Merge)
	}
	if ps.IsIntegrated() {
		t.Fatal("plan should not be integrated until merge runs")
	}

	// Step 2: start performs the merge.
	startOut, err := runBinaryIn(t, bin, dir, "start")
	if err != nil {
		t.Fatalf("start after mark-completed: %v\n%s", err, startOut)
	}
	if !strings.Contains(startOut, "Merge: succeeded") {
		t.Fatalf("expected merge success on start:\n%s", startOut)
	}

	// Target ref advanced to the plan head.
	afterMain := gitOut(t, dir, "rev-parse", "main")
	if afterMain != planHead {
		t.Fatalf("main did not advance to plan head: main=%s planHead=%s\n%s", afterMain, planHead, startOut)
	}

	// Final state: fully integrated.
	project2, err := conductor.LoadProject(dir)
	if err != nil {
		t.Fatalf("reload project: %v", err)
	}
	if !project2.State.Plans["alpha"].IsIntegrated() {
		t.Fatal("plan should be fully integrated after start")
	}
}
