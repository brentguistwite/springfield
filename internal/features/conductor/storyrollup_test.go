package conductor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStoryRollupsAllPresent(t *testing.T) {
	root := t.TempDir()

	// plan-1: 3/5 passed
	writePRDForRollupTest(t, root, "plan-1", 5, 3)
	// plan-2: 0/4 passed
	writePRDForRollupTest(t, root, "plan-2", 4, 0)
	// plan-3: 2/2 passed
	writePRDForRollupTest(t, root, "plan-3", 2, 2)

	units := []PlanUnit{
		{ID: "plan-1", Path: ".springfield/plans/plan-1/prd.json"},
		{ID: "plan-2", Path: ".springfield/plans/plan-2/prd.json"},
		{ID: "plan-3", Path: ".springfield/plans/plan-3/prd.json"},
	}

	rollups := LoadStoryRollups(units, root)

	if len(rollups) != 3 {
		t.Fatalf("want 3 rollups, got %d", len(rollups))
	}

	r1 := rollups["plan-1"]
	if r1.Passed != 3 || r1.Total != 5 || r1.LoadError != "" {
		t.Errorf("plan-1: got %+v", r1)
	}
	r2 := rollups["plan-2"]
	if r2.Passed != 0 || r2.Total != 4 || r2.LoadError != "" {
		t.Errorf("plan-2: got %+v", r2)
	}
	r3 := rollups["plan-3"]
	if r3.Passed != 2 || r3.Total != 2 || r3.LoadError != "" {
		t.Errorf("plan-3: got %+v", r3)
	}
}

func TestLoadStoryRollupsMissingPRD(t *testing.T) {
	root := t.TempDir()

	units := []PlanUnit{
		{ID: "plan-x", Path: ".springfield/plans/plan-x/prd.json"},
	}

	rollups := LoadStoryRollups(units, root)

	r := rollups["plan-x"]
	if r.LoadError == "" {
		t.Error("want non-empty LoadError for missing prd.json")
	}
	if r.Passed != -1 || r.Total != -1 {
		t.Errorf("want -1/-1 sentinel, got %d/%d", r.Passed, r.Total)
	}
}

func TestLoadStoryRollupsMalformedPRD(t *testing.T) {
	root := t.TempDir()

	dir := filepath.Join(root, ".springfield", "plans", "plan-bad")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prd.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	units := []PlanUnit{
		{ID: "plan-bad", Path: ".springfield/plans/plan-bad/prd.json"},
	}

	rollups := LoadStoryRollups(units, root)

	r := rollups["plan-bad"]
	if r.LoadError == "" {
		t.Error("want non-empty LoadError for malformed prd.json")
	}
	if r.Passed != -1 || r.Total != -1 {
		t.Errorf("want -1/-1 sentinel, got %d/%d", r.Passed, r.Total)
	}
}

func TestLoadStoryRollupsEmptyUnits(t *testing.T) {
	root := t.TempDir()
	rollups := LoadStoryRollups(nil, root)
	if len(rollups) != 0 {
		t.Errorf("want empty map, got %d entries", len(rollups))
	}
}

// TestLoadStoryRollupsLegacyPlanUnit verifies that a plan unit whose path is a
// .md file (not prd.json) produces an empty rollup (no error, no story counts).
func TestLoadStoryRollupsLegacyPlanUnit(t *testing.T) {
	root := t.TempDir()

	// Write a .md file at the plan unit's path — simulates a legacy plan
	// registered via "springfield plans add" pointing at a markdown file.
	dir := filepath.Join(root, ".springfield", "plans", "legacy-plan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("# Legacy Plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	units := []PlanUnit{
		{ID: "legacy-plan", Path: ".springfield/plans/legacy-plan/plan.md"},
	}

	rollups := LoadStoryRollups(units, root)

	r := rollups["legacy-plan"]
	if r.LoadError != "" {
		t.Errorf("legacy plan unit must not produce LoadError, got: %q", r.LoadError)
	}
	if r.Passed != 0 || r.Total != 0 {
		t.Errorf("legacy plan unit must produce zero story counts, got Passed=%d Total=%d", r.Passed, r.Total)
	}
}

// writePRDForRollupTest writes a minimal prd.json with nTotal stories, nPassed passed.
func writePRDForRollupTest(t *testing.T, root, planID string, nTotal, nPassed int) {
	t.Helper()
	dir := filepath.Join(root, ".springfield", "plans", planID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	type story struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Passes bool   `json:"passes"`
	}
	type prdDoc struct {
		ID          string  `json:"id"`
		UserStories []story `json:"user_stories"`
	}
	stories := make([]story, nTotal)
	for i := range stories {
		stories[i] = story{
			ID:     fmt.Sprintf("US-%03d", i+1),
			Title:  fmt.Sprintf("story %d", i+1),
			Passes: i < nPassed,
		}
	}
	doc := prdDoc{ID: planID, UserStories: stories}
	data, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "prd.json"), data, 0o644); err != nil {
		t.Fatalf("write prd.json: %v", err)
	}
}
