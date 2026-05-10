package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/prd"
)

// TestPlanFromDir_HappyPath verifies --from-dir loads batch.json from a dir
// and registers both plans just like --prd would.
func TestPlanFromDir_HappyPath(t *testing.T) {
	bin := buildBinary(t)
	projectDir := t.TempDir()
	writeProjectConfig(t, projectDir, "claude")

	// Build the envelope (2 plans).
	env := prd.BatchPRDEnvelope{
		Title:  "from-dir-batch",
		Source: "operator-authored",
		Phases: []prd.PhasePRD{
			{Mode: "serial", Plans: []string{"plan-alpha", "plan-beta"}},
		},
		Plans: []prd.BatchPRDPlan{
			{
				PRD: prd.PRD{
					ID:    "plan-alpha",
					Title: "Alpha",
					UserStories: []prd.UserStory{
						{ID: "US-001", Title: "Story 1", Priority: 1, AcceptanceCriteria: []string{"passes"}},
					},
				},
				ContextMD: "Project uses TypeScript + Bun.",
			},
			{
				PRD: prd.PRD{
					ID:    "plan-beta",
					Title: "Beta",
					UserStories: []prd.UserStory{
						{ID: "US-001", Title: "Story 1", Priority: 1, AcceptanceCriteria: []string{"passes"}},
					},
				},
			},
		},
	}

	// Write batch.json to a temp operator dir.
	opDir := t.TempDir()
	batchJSON, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if err := os.WriteFile(filepath.Join(opDir, "batch.json"), batchJSON, 0o644); err != nil {
		t.Fatalf("write batch.json: %v", err)
	}

	// Run springfield plan --from-dir <opDir>.
	output, err := runBinaryIn(t, bin, projectDir, "plan", "--from-dir", opDir)
	if err != nil {
		t.Fatalf("plan --from-dir: %v\n%s", err, output)
	}

	// .springfield/plans/<batch-id>/batch.json must exist.
	springfieldPlansDir := filepath.Join(projectDir, ".springfield", "plans")
	entries, err := os.ReadDir(springfieldPlansDir)
	if err != nil {
		t.Fatalf("read .springfield/plans: %v", err)
	}
	var batchID string
	for _, e := range entries {
		if e.IsDir() {
			candidate := filepath.Join(springfieldPlansDir, e.Name(), "batch.json")
			if _, err := os.Stat(candidate); err == nil {
				batchID = e.Name()
				break
			}
		}
	}
	if batchID == "" {
		t.Fatalf("no batch dir with batch.json under .springfield/plans/")
	}

	// batch.json has both plan IDs.
	b := readBatchJSON(t, filepath.Join(springfieldPlansDir, batchID, "batch.json"))
	if len(b.PlanIDs) != 2 {
		t.Fatalf("batch.PlanIDs = %v, want 2", b.PlanIDs)
	}

	// Per-plan prd.json files must exist and parse cleanly.
	for _, planID := range []string{"plan-alpha", "plan-beta"} {
		prdPath := filepath.Join(projectDir, ".springfield", "plans", planID, "prd.json")
		if _, err := os.Stat(prdPath); err != nil {
			t.Fatalf("prd.json for %q missing: %v", planID, err)
		}
		if _, err := prd.ParseFile(prdPath); err != nil {
			t.Fatalf("prd.ParseFile(%q): %v", planID, err)
		}
	}

	// PlanUnits must be registered in execution config.
	cfgData, err := os.ReadFile(filepath.Join(projectDir, ".springfield", "execution", "config.json"))
	if err != nil {
		t.Fatalf("read execution config.json: %v", err)
	}
	cfgStr := string(cfgData)
	for _, planID := range []string{"plan-alpha", "plan-beta"} {
		if !strings.Contains(cfgStr, planID) {
			t.Fatalf("execution config.json missing plan %q:\n%s", planID, cfgStr)
		}
	}

	// PlanUnit paths must end in /prd.json.
	type planUnitEntry struct {
		Path string `json:"path"`
	}
	type cfgShape struct {
		PlanUnits []planUnitEntry `json:"plan_units"`
	}
	var cfg cfgShape
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		t.Fatalf("unmarshal execution config: %v", err)
	}
	for _, u := range cfg.PlanUnits {
		if !strings.HasSuffix(u.Path, "/prd.json") {
			t.Fatalf("PlanUnit path %q does not end in /prd.json", u.Path)
		}
	}

	// run.json must be set.
	run := readRunJSON(t, projectDir)
	if run.ActiveBatchID == "" {
		t.Fatalf("run.json active_batch_id is empty")
	}
	if run.ActiveBatchID != batchID {
		t.Fatalf("run.json active_batch_id = %q, want %q", run.ActiveBatchID, batchID)
	}
}

// TestPlanFromDir_RejectsMissingBatchJSON verifies that --from-dir errors when
// batch.json is absent.
func TestPlanFromDir_RejectsMissingBatchJSON(t *testing.T) {
	bin := buildBinary(t)
	projectDir := t.TempDir()
	writeProjectConfig(t, projectDir, "claude")

	emptyDir := t.TempDir()

	output, err := runBinaryIn(t, bin, projectDir, "plan", "--from-dir", emptyDir)
	if err == nil {
		t.Fatalf("expected error when batch.json absent, got success:\n%s", output)
	}
	if !strings.Contains(output, "batch.json") {
		t.Fatalf("expected error mentioning batch.json, got:\n%s", output)
	}
}

// TestPlanFromDir_RejectsUnknownFieldPRD verifies that unknown fields in
// batch.json are rejected (DisallowUnknownFields).
func TestPlanFromDir_RejectsUnknownFieldPRD(t *testing.T) {
	bin := buildBinary(t)
	projectDir := t.TempDir()
	writeProjectConfig(t, projectDir, "claude")

	opDir := t.TempDir()
	// Inject an unknown field "bogus_field" into the envelope.
	bad := `{
  "title": "bad-batch",
  "source": "src",
  "bogus_field": "should fail",
  "phases": [{"mode": "serial", "plans": ["plan-x"]}],
  "plans": [{"id": "plan-x", "title": "X", "user_stories": [{"id": "US-001", "title": "S", "priority": 1, "acceptance_criteria": ["ok"]}]}]
}`
	if err := os.WriteFile(filepath.Join(opDir, "batch.json"), []byte(bad), 0o644); err != nil {
		t.Fatalf("write batch.json: %v", err)
	}

	output, err := runBinaryIn(t, bin, projectDir, "plan", "--from-dir", opDir)
	if err == nil {
		t.Fatalf("expected rejection of unknown field, got success:\n%s", output)
	}
	if !strings.Contains(strings.ToLower(output), "unknown") && !strings.Contains(strings.ToLower(output), "bogus") && !strings.Contains(output, "parse PRD") {
		t.Fatalf("expected parse/unknown-field error, got:\n%s", output)
	}
}

// TestPlanFromDir_MutuallyExclusiveWithPRD verifies --from-dir and --prd
// cannot be used together.
func TestPlanFromDir_MutuallyExclusiveWithPRD(t *testing.T) {
	bin := buildBinary(t)
	projectDir := t.TempDir()
	writeProjectConfig(t, projectDir, "claude")

	opDir := t.TempDir()

	output, err := runBinaryIn(t, bin, projectDir, "plan", "--from-dir", opDir, "--prd", "-")
	if err == nil {
		t.Fatalf("expected error when --from-dir and --prd both set, got success:\n%s", output)
	}
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "from-dir") && !strings.Contains(lower, "prd") {
		t.Fatalf("expected mutual-exclusion error, got:\n%s", output)
	}
}

// Ensure batch package is referenced (avoids unused import if plan_test helpers change).
var _ batch.Batch
