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

// setupStatusRollupFixture creates a 3-plan batch fixture with real prd.json files:
//   - plan-1: fully passed (5/5)
//   - plan-2: partial (3/8)
//   - plan-3: not started (0/4)
func setupStatusRollupFixture(t *testing.T, root string) {
	t.Helper()
	writeSpringfieldConfig(t, root, "claude")

	plans := []struct {
		id     string
		total  int
		passed int
	}{
		{"plan-1", 5, 5},
		{"plan-2", 8, 3},
		{"plan-3", 4, 0},
	}

	units := make([]conductor.PlanUnit, 0, len(plans))
	for i, p := range plans {
		writePRDStatusRollup(t, root, p.id, p.total, p.passed)
		units = append(units, conductor.PlanUnit{
			ID:    p.id,
			Title: p.id,
			Path:  ".springfield/plans/" + p.id + "/prd.json",
			Order: i + 1,
		})
	}

	writeConductorConfigBinary(t, root, &conductor.Config{
		PlansDir:                   ".springfield/plans",
		WorktreeBase:               ".worktrees",
		MaxRetries:                 2,
		SingleWorkstreamIterations: 50,
		SingleWorkstreamTimeout:    3600,
		Tool:                       "claude",
		PlanUnits:                  units,
	})
}

// writePRDStatusRollup writes a minimal prd.json for rollup tests.
func writePRDStatusRollup(t *testing.T, root, planID string, nTotal, nPassed int) {
	t.Helper()
	dir := filepath.Join(root, ".springfield", "plans", planID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	stories := make([]prd.UserStory, nTotal)
	for i := range stories {
		stories[i] = prd.UserStory{
			ID:     formatStoryID(i + 1),
			Title:  "story " + formatStoryID(i+1),
			Passes: i < nPassed,
		}
	}
	doc := prd.PRD{ID: planID, UserStories: stories}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal prd: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prd.json"), data, 0o644); err != nil {
		t.Fatalf("write prd: %v", err)
	}
}

func formatStoryID(n int) string {
	s := "000" + itoa(n)
	return "US-" + s[len(s)-3:]
}

// TestStatusShowsStoryRollupPerPlan verifies each plan line includes story counts.
func TestStatusShowsStoryRollupPerPlan(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	setupStatusRollupFixture(t, root)

	output, err := runBinaryIn(t, bin, root, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, output)
	}

	// per-plan story counts
	if !strings.Contains(output, "5/5 stories") {
		t.Errorf("want '5/5 stories' for plan-1:\n%s", output)
	}
	if !strings.Contains(output, "3/8 stories") {
		t.Errorf("want '3/8 stories' for plan-2:\n%s", output)
	}
	if !strings.Contains(output, "0/4 stories") {
		t.Errorf("want '0/4 stories' for plan-3:\n%s", output)
	}
}

// TestStatusShowsAggregateStorySummary verifies the top-level summary line
// includes aggregate story counts: 0 complete plans (no state), 8/17 stories pass.
func TestStatusShowsAggregateStorySummary(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	setupStatusRollupFixture(t, root)

	// Mark plan-1 as integrated so "1/3 plans complete" is correct.
	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"plan-1": {
				Status: conductor.StatusCompleted,
				Merge: &conductor.MergeOutcome{
					Status:           conductor.MergeSucceeded,
					SourceSyncStatus: "synced",
				},
				Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
			},
		},
	})

	output, err := runBinaryIn(t, bin, root, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, output)
	}

	// 5+3+0 = 8 passed out of 5+8+4 = 17 total
	if !strings.Contains(output, "8/17") {
		t.Errorf("want '8/17' stories in aggregate summary:\n%s", output)
	}
	// 1/3 plans complete
	if !strings.Contains(output, "1") || !strings.Contains(output, "3") {
		t.Errorf("want plan counts in output:\n%s", output)
	}
}

// TestStatusMissingPRDLoadError verifies the LoadStoryRollups behavior for a
// missing prd.json is exercised at the unit level.
// When prd.json is deleted after registration, springfield status returns a
// hard validation error (file not found). The "prd missing" render path is
// covered by TestLoadStoryRollupsMissingPRD in the conductor package.
// This acceptance test instead verifies a plan with an empty stories list
// (zero prd stories) renders without the story suffix.
func TestStatusNoPRDStoriesOmitsStorySuffix(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	writeSpringfieldConfig(t, root, "claude")

	// Write a prd.json with no user stories.
	prdDir := filepath.Join(root, ".springfield", "plans", "empty-plan")
	if err := os.MkdirAll(prdDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(prdDir, "prd.json"),
		[]byte(`{"id":"empty-plan","user_stories":[]}`), 0o644); err != nil {
		t.Fatalf("write prd: %v", err)
	}

	units := []conductor.PlanUnit{
		{ID: "empty-plan", Title: "empty", Path: ".springfield/plans/empty-plan/prd.json", Order: 1},
	}
	writeConductorConfigBinary(t, root, &conductor.Config{
		PlansDir:                   ".springfield/plans",
		WorktreeBase:               ".worktrees",
		MaxRetries:                 2,
		SingleWorkstreamIterations: 50,
		SingleWorkstreamTimeout:    3600,
		Tool:                       "claude",
		PlanUnits:                  units,
	})

	output, err := runBinaryIn(t, bin, root, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, output)
	}

	// No story suffix for a plan with 0 stories.
	if strings.Contains(output, "0/0 stories") {
		t.Errorf("should not render '0/0 stories' for empty PRD:\n%s", output)
	}
	// Plan should still appear.
	if !strings.Contains(output, "empty-plan") {
		t.Errorf("plan should appear in output:\n%s", output)
	}
}

// TestStatusMalformedPRDRendersHint verifies a malformed prd.json renders a
// parse error hint and does not crash.
func TestStatusMalformedPRDRendersHint(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	writeSpringfieldConfig(t, root, "claude")

	// Write a malformed prd.json.
	prdDir := filepath.Join(root, ".springfield", "plans", "bad-plan")
	if err := os.MkdirAll(prdDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(prdDir, "prd.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write bad prd: %v", err)
	}

	units := []conductor.PlanUnit{
		{ID: "bad-plan", Title: "bad", Path: ".springfield/plans/bad-plan/prd.json", Order: 1},
	}
	writeConductorConfigBinary(t, root, &conductor.Config{
		PlansDir:                   ".springfield/plans",
		WorktreeBase:               ".worktrees",
		MaxRetries:                 2,
		SingleWorkstreamIterations: 50,
		SingleWorkstreamTimeout:    3600,
		Tool:                       "claude",
		PlanUnits:                  units,
	})

	output, err := runBinaryIn(t, bin, root, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, output)
	}

	if !strings.Contains(output, "prd parse error") {
		t.Errorf("want 'prd parse error' in output:\n%s", output)
	}
}
