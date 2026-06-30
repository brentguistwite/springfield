package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/prd"
)

// TestSpringfieldStartPerPlanBranchesLeavesStandaloneBranches is the per-plan
// mode end-to-end black-box: a 2-plan batch run with --per-plan-branches must
// leave one standalone springfield/<plan> branch per plan, merge NOTHING into
// the base, deregister its own plan units, and archive a per-ticket record
// carrying each plan's branch, base, and durable evidence path.
func TestSpringfieldStartPerPlanBranchesLeavesStandaloneBranches(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")
	beforeMain := gitOut(t, dir, "rev-parse", "main")

	story := []prd.UserStory{{
		ID: "US-001", Title: "do it", Description: "do it",
		AcceptanceCriteria: []string{"passes"}, Priority: 1,
	}}
	env := prd.BatchPRDEnvelope{
		Title:  "Per-plan batch",
		Source: "per-plan batch",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-1", "plan-2"}}},
		Plans: []prd.BatchPRDPlan{
			{PRD: prd.PRD{ID: "plan-1", Title: "Plan One", UserStories: story}},
			{PRD: prd.PRD{ID: "plan-2", Title: "Plan Two", UserStories: story}},
		},
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if out, perr := planWithPRD(t, bin, dir, string(data)); perr != nil {
		t.Fatalf("plan: %v\n%s", perr, out)
	}

	fakeBinDir := filepath.Join(dir, "bin")
	installPRDFakeAgentBinary(t, fakeBinDir, "claude", []string{"US-001"})

	output, err := runBinaryInWithEnv(t, bin, dir,
		[]string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")},
		"start", "--per-plan-branches")
	if err != nil {
		t.Fatalf("start --per-plan-branches: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Status: completed") {
		t.Fatalf("expected completion, got:\n%s", output)
	}
	if !strings.Contains(output, "Branch mode: per-plan") {
		t.Fatalf("expected per-plan branch-mode line, got:\n%s", output)
	}

	// Nothing merged into the base: main must be exactly where it started.
	if after := gitOut(t, dir, "rev-parse", "main"); after != beforeMain {
		t.Fatalf("per-plan mode must not advance base; main before=%s after=%s", beforeMain, after)
	}

	// Both plan branches are retained, each carrying its own agent commit
	// (diverged from main).
	for _, br := range []string{"springfield/plan-1", "springfield/plan-2"} {
		if strings.TrimSpace(gitOut(t, dir, "branch", "--list", br)) == "" {
			t.Fatalf("standalone branch %s missing:\n%s", br, output)
		}
		head := gitOut(t, dir, "rev-parse", br)
		if head == beforeMain {
			t.Fatalf("branch %s should have diverged from main (agent commit), got base head", br)
		}
	}

	// Worktrees removed on clean success.
	for _, p := range []string{"plan-1", "plan-2"} {
		if _, statErr := os.Stat(filepath.Join(dir, ".worktrees", p)); !os.IsNotExist(statErr) {
			t.Fatalf("worktree %s should be removed, stat err=%v", p, statErr)
		}
	}

	// status --json reports the archived batch with one per-ticket card each
	// carrying branch + base + durable evidence path.
	jsonOut, err := runBinaryIn(t, bin, dir, "status", "--json")
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, jsonOut)
	}
	var view struct {
		State string `json:"state"`
		Plans []struct {
			ID           string `json:"id"`
			Status       string `json:"status"`
			Branch       string `json:"branch"`
			BaseBranch   string `json:"base_branch"`
			EvidencePath string `json:"evidence_path"`
		} `json:"plans"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &view); err != nil {
		t.Fatalf("decode status json: %v\n%s", err, jsonOut)
	}
	if view.State != "archived" {
		t.Fatalf("state = %q, want archived\n%s", view.State, jsonOut)
	}
	if len(view.Plans) != 2 {
		t.Fatalf("want 2 archived plan cards, got %d\n%s", len(view.Plans), jsonOut)
	}
	for _, pv := range view.Plans {
		if pv.Branch != "springfield/"+pv.ID {
			t.Fatalf("plan %s branch = %q, want springfield/%s", pv.ID, pv.Branch, pv.ID)
		}
		if pv.BaseBranch != "main" {
			t.Fatalf("plan %s base = %q, want main", pv.ID, pv.BaseBranch)
		}
		if pv.EvidencePath == "" {
			t.Fatalf("plan %s evidence_path must be set", pv.ID)
		}
		// Durable evidence is readable at the archived path.
		if _, statErr := os.Stat(filepath.Join(dir, pv.EvidencePath)); statErr != nil {
			t.Fatalf("plan %s durable evidence not readable at %s: %v", pv.ID, pv.EvidencePath, statErr)
		}
	}

	// Fix 2: the batch's own plan units are deregistered.
	cfgBytes, err := os.ReadFile(filepath.Join(dir, ".springfield", "execution", "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg struct {
		PlanUnits []struct {
			ID string `json:"id"`
		} `json:"plan_units"`
	}
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	for _, u := range cfg.PlanUnits {
		if u.ID == "plan-1" || u.ID == "plan-2" {
			t.Fatalf("batch unit %s must be deregistered after completion", u.ID)
		}
	}
}
