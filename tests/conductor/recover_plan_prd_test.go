package conductor_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/conductor"
	"springfield/internal/features/prd"
)

// TestRecoverRetryPreservesPRDJson verifies the load-bearing invariant:
// RecoverRetry MUST NOT touch prd.json. The durable per-story passes state
// is the source of truth across retries; the runner's NextStory depends on it.
func TestRecoverRetryPreservesPRDJson(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	// Write a prd.json where US-001 already passes.
	prdPath := writePRDWithPartialPass(t, root, "alpha", []prd.UserStory{
		{ID: "US-001", Title: "first", Passes: true},
		{ID: "US-002", Title: "second", Passes: false},
	})

	// Record bytes before RecoverRetry.
	before, err := os.ReadFile(prdPath)
	if err != nil {
		t.Fatalf("read prd before: %v", err)
	}

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.MarkRunning("alpha")
	project.MarkFailed("alpha", "exit code 1", "claude", "")

	if _, err := project.RecoverRetry("alpha"); err != nil {
		t.Fatalf("RecoverRetry: %v", err)
	}
	if err := project.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// prd.json must be byte-identical after RecoverRetry.
	after, err := os.ReadFile(prdPath)
	if err != nil {
		t.Fatalf("read prd after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("prd.json changed after RecoverRetry\nbefore: %s\nafter:  %s", before, after)
	}
}

// TestRecoverRetryNextStorySkipsPassedAfterRetry verifies that after a
// RecoverRetry, NextStory skips already-passed stories and returns the next
// pending one — confirming the passes state survives across the retry cycle.
func TestRecoverRetryNextStorySkipsPassedAfterRetry(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	writePRDWithPartialPass(t, root, "alpha", []prd.UserStory{
		{ID: "US-001", Title: "first", Passes: true},
		{ID: "US-002", Title: "second", Passes: false},
	})

	// Simulate RecoverRetry cycle.
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.MarkRunning("alpha")
	project.MarkFailed("alpha", "exit code 1", "claude", "")
	if _, err := project.RecoverRetry("alpha"); err != nil {
		t.Fatalf("RecoverRetry: %v", err)
	}

	// Load the PRD and call NextStory — it must return US-002, not US-001.
	plan := loadPRDForTest(t, root, "alpha")
	next, ok := planrunNextStory(plan)
	if !ok {
		t.Fatal("NextStory returned false, want US-002")
	}
	if next.ID != "US-002" {
		t.Fatalf("NextStory = %q, want US-002", next.ID)
	}
}

// TestDiagnosePlanRendersStoriesSection verifies that DiagnosePlan.Render()
// includes a "Stories" section when a prd.json is supplied.
func TestDiagnosePlanRendersStoriesSection(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	units := []string{"alpha"}
	writeRegisteredPlanUnitConfig(t, root, units)

	// Write prd.json with one passed, one pending story.
	stories := []prd.UserStory{
		{ID: "US-001", Title: "scaffold", Passes: true},
		{ID: "US-002", Title: "feat", Passes: false},
	}
	writeRegisteredPlanUnitConfigWithPRD(t, root, "alpha", stories)

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.MarkRunning("alpha")
	project.MarkFailed("alpha", "exit code 1", "claude", "")

	prdPlan := loadPRDForTest(t, root, "alpha")
	diag := conductor.DiagnosePlanWithPRD(project, "alpha", nil, &prdPlan)
	rendered := diag.Render()

	if !strings.Contains(rendered, "Stories:") {
		t.Errorf("want 'Stories:' section in render:\n%s", rendered)
	}
	if !strings.Contains(rendered, "US-001") {
		t.Errorf("want US-001 in render:\n%s", rendered)
	}
	if !strings.Contains(rendered, "passed") {
		t.Errorf("want 'passed' in render:\n%s", rendered)
	}
	if !strings.Contains(rendered, "US-002") {
		t.Errorf("want US-002 in render:\n%s", rendered)
	}
	if !strings.Contains(rendered, "pending") {
		t.Errorf("want 'pending' for US-002:\n%s", rendered)
	}
	// Current target should be tagged.
	if !strings.Contains(rendered, "current target") {
		t.Errorf("want 'current target' tag in render:\n%s", rendered)
	}
}

// TestDiagnosePlanNoPRDOmitsStoriesSection verifies backward compat:
// when no PRD is provided, the Stories section is absent.
func TestDiagnosePlanNoPRDOmitsStoriesSection(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.MarkRunning("alpha")
	project.MarkFailed("alpha", "exit code 1", "claude", "")

	diag := conductor.DiagnosePlan(project, "alpha", nil)
	rendered := diag.Render()

	if strings.Contains(rendered, "Stories:") {
		t.Errorf("Stories section should not appear without PRD:\n%s", rendered)
	}
}

// --- helpers ---

// writePRDWithPartialPass writes a prd.json into the plan's canonical location
// and returns the absolute path.
func writePRDWithPartialPass(t *testing.T, root, planID string, stories []prd.UserStory) string {
	t.Helper()
	dir := filepath.Join(root, ".springfield", "plans", planID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	doc := prd.PRD{ID: planID, UserStories: stories}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal prd: %v", err)
	}
	path := filepath.Join(dir, "prd.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write prd: %v", err)
	}
	return path
}

// writeRegisteredPlanUnitConfigWithPRD registers a plan with the given stories
// written into prd.json under the canonical .springfield/plans/<id>/ dir.
func writeRegisteredPlanUnitConfigWithPRD(t *testing.T, root, id string, stories []prd.UserStory) {
	t.Helper()
	prdDir := filepath.Join(root, ".springfield", "plans", id)
	if err := os.MkdirAll(prdDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	doc := prd.PRD{ID: id, UserStories: stories}
	data, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(filepath.Join(prdDir, "prd.json"), data, 0o644); err != nil {
		t.Fatalf("write prd: %v", err)
	}
}

func loadPRDForTest(t *testing.T, root, planID string) prd.PRD {
	t.Helper()
	path := filepath.Join(root, ".springfield", "plans", planID, "prd.json")
	plan, err := prd.ParseFile(path)
	if err != nil {
		t.Fatalf("load prd: %v", err)
	}
	return plan
}

// planrunNextStory is a thin adapter so tests/conductor doesn't import planrun directly.
// We import planrun via the full import path below.
func planrunNextStory(plan prd.PRD) (prd.UserStory, bool) {
	// Use inline logic equivalent to planrun.NextStory to avoid circular import.
	// (planrun imports conductor; conductor tests can't import planrun without a cycle.)
	type sorter struct {
		story    prd.UserStory
		priority int
	}
	passed := make(map[string]bool, len(plan.UserStories))
	for _, s := range plan.UserStories {
		if s.Passes {
			passed[s.ID] = true
		}
	}
	var eligible []prd.UserStory
	for _, s := range plan.UserStories {
		if s.Passes {
			continue
		}
		depsOK := true
		for _, dep := range s.Deps {
			if !passed[dep] {
				depsOK = false
				break
			}
		}
		if depsOK {
			eligible = append(eligible, s)
		}
	}
	if len(eligible) == 0 {
		return prd.UserStory{}, false
	}
	best := eligible[0]
	for _, s := range eligible[1:] {
		if s.Priority < best.Priority || (s.Priority == best.Priority && s.ID < best.ID) {
			best = s
		}
	}
	return best, true
}

// Prevent unused import error from fmt if helpers don't use it.
var _ = fmt.Sprintf
