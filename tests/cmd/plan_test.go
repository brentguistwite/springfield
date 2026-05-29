package cmd_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/prd"
)

// --- helpers ---

// minPRDStory returns a valid UserStory for test envelopes.
func minPRDStory(id string) prd.UserStory {
	return prd.UserStory{
		ID:                 id,
		Title:              "Story " + id,
		Priority:           1,
		AcceptanceCriteria: []string{"passes"},
	}
}

// minPRDPlan returns a valid BatchPRDPlan for test envelopes.
func minPRDPlan(id, title string) prd.BatchPRDPlan {
	return prd.BatchPRDPlan{
		PRD: prd.PRD{
			ID:          id,
			Title:       title,
			UserStories: []prd.UserStory{minPRDStory("US-001")},
		},
	}
}

// buildEnvelopeJSON marshals a BatchPRDEnvelope to JSON or fatals.
func buildEnvelopeJSON(t *testing.T, env prd.BatchPRDEnvelope) string {
	t.Helper()
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return string(data)
}

// planWithPRD runs "springfield plan --prd -" piping envJSON via stdin.
// Returns combined stdout+stderr output and error.
func planWithPRD(t *testing.T, bin, dir string, envJSON string, extraArgs ...string) (string, error) {
	t.Helper()
	args := append([]string{"plan", "--prd", "-"}, extraArgs...)
	return runBinaryInWithInput(t, bin, dir, envJSON, args...)
}

// runPlanSplit runs "springfield plan --prd -" with separated stderr.
// Returns stdout, stderr, and error.
func runPlanSplit(t *testing.T, bin, dir, envJSON string, extraArgs ...string) (stdout, stderr string, err error) {
	t.Helper()
	args := append([]string{"plan", "--prd", "-"}, extraArgs...)
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(envJSON)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// writeProjectConfig writes springfield.toml and execution/config.json for plan tests.
func writeProjectConfig(t *testing.T, dir, agent string) {
	t.Helper()
	writeSpringfieldConfig(t, dir, agent)
	// Write conductor config so LoadProject succeeds.
	cfg := map[string]any{
		"plans_dir":                    ".springfield/plans",
		"worktree_base":                ".worktrees",
		"max_retries":                  2,
		"single_workstream_iterations": 50,
		"single_workstream_timeout":    3600,
		"tool":                         agent,
	}
	cfgPath := filepath.Join(dir, ".springfield", "execution", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal cfg: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
}

// readBatchJSON reads and decodes batch.json at the given path.
func readBatchJSON(t *testing.T, path string) batch.Batch {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read batch.json at %s: %v", path, err)
	}
	var b batch.Batch
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("decode batch.json: %v", err)
	}
	return b
}

// readRunJSON reads and decodes run.json.
func readRunJSON(t *testing.T, dir string) batch.Run {
	t.Helper()
	path := filepath.Join(dir, ".springfield", "run.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read run.json: %v", err)
	}
	var r batch.Run
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("decode run.json: %v", err)
	}
	return r
}

// --- tests ---

func TestSpringfieldPlanHelp(t *testing.T) {
	output, err := runSpringfield(t, "plan", "--help")
	if err != nil {
		t.Fatalf("plan --help failed: %v\n%s", err, output)
	}

	for _, marker := range []string{
		"--prd",
		"--replace",
		"--append",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected plan help to contain %q, got:\n%s", marker, output)
		}
	}

	// --slices must be gone
	if strings.Contains(output, "--slices") {
		t.Fatalf("--slices flag must be removed from plan help, got:\n%s", output)
	}
}

func TestSpringfieldPlanRequiresPRDFlag(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	output, err := runBinaryIn(t, bin, dir, "plan")
	if err == nil {
		t.Fatalf("expected error when --prd missing, got:\n%s", output)
	}
	if !strings.Contains(output, "--prd is required") {
		t.Fatalf("expected '--prd is required' error, got:\n%s", output)
	}
}

// TestPlanCompilesPRDEnvelope is the happy path: 2 plans / 5 stories.
// Asserts all per-plan dirs, prd.json files, PlanUnits in springfield.toml,
// and run.json cursor are created correctly.
func TestPlanCompilesPRDEnvelope(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	env := prd.BatchPRDEnvelope{
		Title:  "my-batch",
		Source: "test-source",
		Phases: []prd.PhasePRD{
			{Mode: "serial", Plans: []string{"plan-alpha", "plan-beta"}},
		},
		Plans: []prd.BatchPRDPlan{
			func() prd.BatchPRDPlan {
				p := minPRDPlan("plan-alpha", "Alpha Plan")
				p.UserStories = append(p.UserStories,
					minPRDStory("US-002"),
					minPRDStory("US-003"),
				)
				return p
			}(),
			func() prd.BatchPRDPlan {
				p := minPRDPlan("plan-beta", "Beta Plan")
				p.UserStories = append(p.UserStories,
					minPRDStory("US-002"),
				)
				return p
			}(),
		},
	}
	envJSON := buildEnvelopeJSON(t, env)

	output, err := planWithPRD(t, bin, dir, envJSON)
	if err != nil {
		t.Fatalf("springfield plan --prd -: %v\n%s", err, output)
	}

	// batch.json must exist under .springfield/plans/<batch-id>/
	springfieldPlansDir := filepath.Join(dir, ".springfield", "plans")
	entries, err := os.ReadDir(springfieldPlansDir)
	if err != nil {
		t.Fatalf("read .springfield/plans: %v", err)
	}
	// Find the batch dir (contains batch.json)
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
		t.Fatalf("no batch dir with batch.json found under .springfield/plans/")
	}

	// batch.json has both plan IDs
	b := readBatchJSON(t, filepath.Join(springfieldPlansDir, batchID, "batch.json"))
	if len(b.PlanIDs) != 2 {
		t.Fatalf("batch.PlanIDs = %v, want 2 entries", b.PlanIDs)
	}

	// Per-plan prd.json exists and parses cleanly for each plan
	for _, planID := range []string{"plan-alpha", "plan-beta"} {
		prdPath := filepath.Join(dir, ".springfield", "plans", planID, "prd.json")
		if _, err := os.Stat(prdPath); err != nil {
			t.Fatalf("prd.json for %q missing at %s: %v", planID, prdPath, err)
		}
		if _, err := prd.ParseFile(prdPath); err != nil {
			t.Fatalf("prd.ParseFile(%q): %v", prdPath, err)
		}
	}

	// execution/config.json PlanUnits must contain both plans
	cfgData, err := os.ReadFile(filepath.Join(dir, ".springfield", "execution", "config.json"))
	if err != nil {
		t.Fatalf("read execution config.json: %v", err)
	}
	cfgStr := string(cfgData)
	for _, planID := range []string{"plan-alpha", "plan-beta"} {
		if !strings.Contains(cfgStr, planID) {
			t.Fatalf("execution config.json missing plan %q:\n%s", planID, cfgStr)
		}
	}

	// run.json must have active_batch_id set
	run := readRunJSON(t, dir)
	if run.ActiveBatchID == "" {
		t.Fatalf("run.json active_batch_id is empty")
	}
	if run.ActiveBatchID != batchID {
		t.Fatalf("run.json active_batch_id = %q, want %q", run.ActiveBatchID, batchID)
	}
}

// TestPlanReplaceArchivesPriorBatch verifies --replace archives prior and activates new.
func TestPlanReplaceArchivesPriorBatch(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	env1 := prd.BatchPRDEnvelope{
		Title:  "first-batch",
		Source: "src1",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-one"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-one", "Plan One")},
	}
	out1, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env1))
	if err != nil {
		t.Fatalf("first plan: %v\n%s", err, out1)
	}

	// Get first batch id
	run1 := readRunJSON(t, dir)
	batchID1 := run1.ActiveBatchID

	// Run plan again with --replace
	env2 := prd.BatchPRDEnvelope{
		Title:  "second-batch",
		Source: "src2",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-two"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-two", "Plan Two")},
	}
	out2, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env2), "--replace")
	if err != nil {
		t.Fatalf("replace plan: %v\n%s", err, out2)
	}

	// Archive entry must exist for prior batch
	archivePath := filepath.Join(dir, ".springfield", "archive", batchID1+".json")
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("archive entry missing for %s: %v", batchID1, err)
	}

	// New batch is active
	run2 := readRunJSON(t, dir)
	if run2.ActiveBatchID == batchID1 {
		t.Fatalf("active_batch_id still %q after replace", batchID1)
	}
	if run2.ActiveBatchID == "" {
		t.Fatalf("active_batch_id empty after replace")
	}

	// New plan's prd.json exists
	prdPath := filepath.Join(dir, ".springfield", "plans", "plan-two", "prd.json")
	if _, err := os.Stat(prdPath); err != nil {
		t.Fatalf("prd.json for plan-two missing: %v", err)
	}
}

// TestPlanAppendAddsNewPlans verifies --append adds plans to existing batch.
func TestPlanAppendAddsNewPlans(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	// Initial 1-plan batch
	env1 := prd.BatchPRDEnvelope{
		Title:  "append-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-one"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-one", "Plan One")},
	}
	out1, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env1))
	if err != nil {
		t.Fatalf("initial plan: %v\n%s", err, out1)
	}
	batchID := readRunJSON(t, dir).ActiveBatchID

	// Append 1 new plan
	env2 := prd.BatchPRDEnvelope{
		Title:  "extra",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-two"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-two", "Plan Two")},
	}
	out2, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env2), "--append")
	if err != nil {
		t.Fatalf("append plan: %v\n%s", err, out2)
	}

	// batch.json must now have 2 plan IDs
	batchPath := filepath.Join(dir, ".springfield", "plans", batchID, "batch.json")
	b := readBatchJSON(t, batchPath)
	if len(b.PlanIDs) != 2 {
		t.Fatalf("after append batch.PlanIDs = %v, want 2", b.PlanIDs)
	}

	// Both per-plan dirs must exist
	for _, planID := range []string{"plan-one", "plan-two"} {
		prdPath := filepath.Join(dir, ".springfield", "plans", planID, "prd.json")
		if _, err := os.Stat(prdPath); err != nil {
			t.Fatalf("prd.json for %q missing: %v", planID, err)
		}
	}

	// execution/config.json must contain both plan units after append
	cfgData, err := os.ReadFile(filepath.Join(dir, ".springfield", "execution", "config.json"))
	if err != nil {
		t.Fatalf("read execution config.json: %v", err)
	}
	cfgStr := string(cfgData)
	for _, planID := range []string{"plan-one", "plan-two"} {
		if !strings.Contains(cfgStr, planID) {
			t.Fatalf("execution config.json missing %q after append:\n%s", planID, cfgStr)
		}
	}
}

// TestPlanAppendPreservesSourceMD verifies that --append does not overwrite the
// original source.md written by the first plan invocation. The append path
// previously passed the new envelope's source into WriteBatch, silently
// discarding the original batch provenance.
func TestPlanAppendPreservesSourceMD(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	originalSource := "## Original Batch\n\nThis is the original batch source."
	env1 := prd.BatchPRDEnvelope{
		Title:  "preserve-source-batch",
		Source: originalSource,
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-one"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-one", "Plan One")},
	}
	out1, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env1))
	if err != nil {
		t.Fatalf("initial plan: %v\n%s", err, out1)
	}
	batchID := readRunJSON(t, dir).ActiveBatchID
	sourcePath := filepath.Join(dir, ".springfield", "plans", batchID, "source.md")

	// Verify initial source.md content.
	got, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read initial source.md: %v", err)
	}
	if string(got) != originalSource {
		t.Fatalf("initial source.md = %q, want %q", string(got), originalSource)
	}

	// Append a second plan with a different source.
	env2 := prd.BatchPRDEnvelope{
		Title:  "extra",
		Source: "## Append Source\n\nThis is the append source and must NOT overwrite source.md.",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-two"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-two", "Plan Two")},
	}
	out2, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env2), "--append")
	if err != nil {
		t.Fatalf("append plan: %v\n%s", err, out2)
	}

	// source.md must still contain the original content.
	after, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read source.md after append: %v", err)
	}
	if string(after) != originalSource {
		t.Fatalf("source.md changed after --append:\nwant: %q\n got: %q", originalSource, string(after))
	}
}

// TestPlanAppendRejectsCollidingPlanID verifies --append rejects duplicate plan IDs.
func TestPlanAppendRejectsCollidingPlanID(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	// Create batch with plan-one
	env := prd.BatchPRDEnvelope{
		Title:  "collision-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-one"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-one", "Plan One")},
	}
	out1, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env))
	if err != nil {
		t.Fatalf("initial plan: %v\n%s", err, out1)
	}

	// Append with same plan-one ID
	out2, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env), "--append")
	if err == nil {
		t.Fatalf("expected collision error, got success:\n%s", out2)
	}
	if !strings.Contains(out2, "plan-one") || !strings.Contains(strings.ToLower(out2), "collision") && !strings.Contains(strings.ToLower(out2), "already") && !strings.Contains(strings.ToLower(out2), "exist") {
		t.Fatalf("expected collision error mentioning plan-one, got:\n%s", out2)
	}
}

// TestPlanRefusesWithActiveBatch verifies default behavior with existing batch.
func TestPlanRefusesWithActiveBatch(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	env := prd.BatchPRDEnvelope{
		Title:  "existing-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-one"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-one", "Plan One")},
	}
	envJSON := buildEnvelopeJSON(t, env)

	out1, err := planWithPRD(t, bin, dir, envJSON)
	if err != nil {
		t.Fatalf("initial plan: %v\n%s", err, out1)
	}

	// Second run without --replace or --append must fail
	env2 := prd.BatchPRDEnvelope{
		Title:  "new-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-two"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-two", "Plan Two")},
	}
	out2, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env2))
	if err == nil {
		t.Fatalf("expected conflict error, got success:\n%s", out2)
	}
	if !strings.Contains(out2, "--replace") && !strings.Contains(out2, "--append") {
		t.Fatalf("expected mention of --replace or --append, got:\n%s", out2)
	}
}

// TestPlanRejectsLegacySliceShape verifies the two-pass legacy detection.
func TestPlanRejectsLegacySliceShape(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	legacyPayload := `{"title":"old batch","source":"cli","slices":[{"id":"s1","title":"Slice 1","instructions":"do it"}]}`

	output, err := planWithPRD(t, bin, dir, legacyPayload)
	if err == nil {
		t.Fatalf("expected rejection of legacy slice shape, got success:\n%s", output)
	}
	if !strings.Contains(output, "legacy single-slice batch detected") {
		t.Fatalf("expected legacy rejection message, got:\n%s", output)
	}
}

// TestPlanSurfacesWarnings verifies that context_md > 32KB produces a [warn] on stderr.
func TestPlanSurfacesWarnings(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	// 33KB context_md triggers warning
	largeContext := strings.Repeat("x", 33*1024)
	env := prd.BatchPRDEnvelope{
		Title:  "warn-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-warn"}}},
		Plans: []prd.BatchPRDPlan{
			{
				PRD:       prd.PRD{ID: "plan-warn", Title: "Warn Plan", UserStories: []prd.UserStory{minPRDStory("US-001")}},
				ContextMD: largeContext,
			},
		},
	}
	envJSON := buildEnvelopeJSON(t, env)

	_, stderr, err := runPlanSplit(t, bin, dir, envJSON)
	if err != nil {
		t.Fatalf("expected success with warning, got error: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "[warn]") {
		t.Fatalf("expected [warn] on stderr, got:\n%s", stderr)
	}

	// prd.json must still be written
	prdPath := filepath.Join(dir, ".springfield", "plans", "plan-warn", "prd.json")
	if _, err := os.Stat(prdPath); err != nil {
		t.Fatalf("prd.json missing after warning: %v", err)
	}
}

// TestPlanRejectsInvalidEnvelope verifies hard errors from prd.Validate surface.
func TestPlanRejectsInvalidEnvelope(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	cases := []struct {
		name      string
		env       prd.BatchPRDEnvelope
		wantInErr string
	}{
		{
			name: "dangling phase reference",
			env: prd.BatchPRDEnvelope{
				Title:  "bad",
				Source: "src",
				Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"ghost-plan"}}},
				Plans:  []prd.BatchPRDPlan{minPRDPlan("real-plan", "Real")},
			},
			wantInErr: "ghost-plan",
		},
		{
			name: "duplicate plan IDs",
			env: prd.BatchPRDEnvelope{
				Title:  "bad",
				Source: "src",
				Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-dup"}}},
				Plans: []prd.BatchPRDPlan{
					minPRDPlan("plan-dup", "Dup A"),
					minPRDPlan("plan-dup", "Dup B"),
				},
			},
			wantInErr: "plan-dup",
		},
		{
			name: "cross-plan dep (dep belongs to another plan)",
			env: prd.BatchPRDEnvelope{
				Title:  "cross-dep",
				Source: "src",
				Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-a", "plan-b"}}},
				Plans: []prd.BatchPRDPlan{
					{
						PRD: prd.PRD{
							ID:    "plan-a",
							Title: "Plan A",
							UserStories: []prd.UserStory{
								// US-002 exists only in plan-b, not plan-a
								{ID: "US-001", Title: "Story 1", Priority: 1, AcceptanceCriteria: []string{"ok"}, Deps: []string{"US-002"}},
							},
						},
					},
					{
						PRD: prd.PRD{
							ID:    "plan-b",
							Title: "Plan B",
							UserStories: []prd.UserStory{
								{ID: "US-002", Title: "Story 2", Priority: 1, AcceptanceCriteria: []string{"ok"}},
							},
						},
					},
				},
			},
			wantInErr: "US-002",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, tc.env))
			if err == nil {
				t.Fatalf("expected error for %q, got success:\n%s", tc.name, output)
			}
			if !strings.Contains(output, tc.wantInErr) {
				t.Fatalf("expected error to contain %q, got:\n%s", tc.wantInErr, output)
			}
		})
	}
}

// TestPlanFromFile verifies --prd <path> reads from a file (not stdin).
func TestPlanFromFile(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	env := prd.BatchPRDEnvelope{
		Title:  "file-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-file"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-file", "File Plan")},
	}
	envJSON := buildEnvelopeJSON(t, env)

	// Write to a temp file
	prdFile := filepath.Join(dir, "my-batch.json")
	if err := os.WriteFile(prdFile, []byte(envJSON), 0o644); err != nil {
		t.Fatalf("write prd file: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "plan", "--prd", prdFile)
	if err != nil {
		t.Fatalf("plan --prd <file>: %v\n%s", err, output)
	}

	// prd.json must exist
	prdPath := filepath.Join(dir, ".springfield", "plans", "plan-file", "prd.json")
	if _, err := os.Stat(prdPath); err != nil {
		t.Fatalf("prd.json missing: %v", err)
	}
}

// writeRunningPlanState injects a StatusRunning plan state into conductor state.json
// so the plan command sees it. Returns the planID.
func writeRunningPlanState(t *testing.T, dir, planID string) {
	t.Helper()
	project, err := conductor.LoadProjectRaw(dir)
	if err != nil {
		t.Fatalf("LoadProjectRaw: %v", err)
	}
	project.State.Plans[planID] = &conductor.PlanState{
		Status: conductor.StatusRunning,
	}
	if err := project.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
}

// TestPlanAppendRefusesWhenPlanIsRunning verifies --append is rejected when a plan
// is currently running (Status == StatusRunning in conductor state).
func TestPlanAppendRefusesWhenPlanIsRunning(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	env := prd.BatchPRDEnvelope{
		Title:  "running-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-one"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-one", "Plan One")},
	}
	out1, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env))
	if err != nil {
		t.Fatalf("initial plan: %v\n%s", err, out1)
	}

	// Inject a running plan state so the guard fires.
	writeRunningPlanState(t, dir, "plan-one")

	env2 := prd.BatchPRDEnvelope{
		Title:  "extra",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-two"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-two", "Plan Two")},
	}
	output, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env2), "--append")
	if err == nil {
		t.Fatalf("expected --append to fail when plan is running, got success:\n%s", output)
	}
	if !strings.Contains(output, "running") {
		t.Fatalf("error should mention 'running', got:\n%s", output)
	}
	if !strings.Contains(output, "plan-one") {
		t.Fatalf("error should mention the running plan ID, got:\n%s", output)
	}
}

// TestPlanReplaceRefusesWhenPlanIsRunning verifies --replace is rejected when a plan
// is currently running (Status == StatusRunning in conductor state).
func TestPlanReplaceRefusesWhenPlanIsRunning(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	env := prd.BatchPRDEnvelope{
		Title:  "running-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-one"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-one", "Plan One")},
	}
	out1, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env))
	if err != nil {
		t.Fatalf("initial plan: %v\n%s", err, out1)
	}

	// Inject a running plan state so the guard fires.
	writeRunningPlanState(t, dir, "plan-one")

	env2 := prd.BatchPRDEnvelope{
		Title:  "replacement",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-two"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-two", "Plan Two")},
	}
	output, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env2), "--replace")
	if err == nil {
		t.Fatalf("expected --replace to fail when plan is running, got success:\n%s", output)
	}
	if !strings.Contains(output, "running") {
		t.Fatalf("error should mention 'running', got:\n%s", output)
	}
	if !strings.Contains(output, "plan-one") {
		t.Fatalf("error should mention the running plan ID, got:\n%s", output)
	}
}

// TestPlanReplaceAllowsRunningPlanThatIsPreserved verifies the narrow shape
// of the --replace running-plan guard: a running plan whose unit is PRESERVED
// in the new envelope is NOT affected by --replace (its registration stays,
// the runner's state record is untouched), so blocking it would be overly
// conservative. The guard only blocks when the running plan would be REMOVED
// — the case where the registry would actually drift out from under the live
// runner.
func TestPlanReplaceAllowsRunningPlanThatIsPreserved(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	// Initial batch: two plans, kept-plan + dropped-plan.
	env := prd.BatchPRDEnvelope{
		Title:  "initial-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"kept-plan", "dropped-plan"}}},
		Plans: []prd.BatchPRDPlan{
			minPRDPlan("kept-plan", "Kept Plan"),
			minPRDPlan("dropped-plan", "Dropped Plan"),
		},
	}
	if out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env)); err != nil {
		t.Fatalf("initial plan: %v\n%s", err, out)
	}

	// kept-plan is currently running.
	writeRunningPlanState(t, dir, "kept-plan")

	// Replacement envelope drops "dropped-plan", keeps "kept-plan", adds "new-plan".
	env2 := prd.BatchPRDEnvelope{
		Title:  "replacement",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"kept-plan", "new-plan"}}},
		Plans: []prd.BatchPRDPlan{
			minPRDPlan("kept-plan", "Kept Plan"),
			minPRDPlan("new-plan", "New Plan"),
		},
	}
	out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env2), "--replace")
	if err != nil {
		t.Fatalf("expected --replace to SUCCEED when running plan is preserved in the new envelope, got error:\n%s\n%v", out, err)
	}
}

// TestPlanReplacePicksUpDriftedFieldsOnPreservedID pins the drift-detection
// half of preserved-unit handling: when --replace keeps a plan ID but the new
// envelope changes Path/Title/Order, the on-disk PlanUnit record must reflect
// the new envelope, not the prior registration. A naive "skip add for known
// IDs" implementation would silently keep stale fields — this test locks in
// the drop-and-re-add path.
func TestPlanReplacePicksUpDriftedFieldsOnPreservedID(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	env1 := prd.BatchPRDEnvelope{
		Title:  "initial-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"shared-plan"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("shared-plan", "Initial Title")},
	}
	if out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env1)); err != nil {
		t.Fatalf("initial plan: %v\n%s", err, out)
	}

	env2 := prd.BatchPRDEnvelope{
		Title:  "replacement",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"shared-plan"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("shared-plan", "Updated Title")},
	}
	if out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env2), "--replace"); err != nil {
		t.Fatalf("replace with same ID + new title: %v\n%s", err, out)
	}

	project, err := conductor.LoadProjectRaw(dir)
	if err != nil {
		t.Fatalf("LoadProjectRaw: %v", err)
	}
	var got *conductor.PlanUnit
	for i := range project.Config.PlanUnits {
		if project.Config.PlanUnits[i].ID == "shared-plan" {
			got = &project.Config.PlanUnits[i]
			break
		}
	}
	if got == nil {
		t.Fatal("shared-plan unit missing after --replace")
	}
	if got.Title != "Updated Title" {
		t.Fatalf("PlanUnit.Title = %q, want %q (drift on preserved ID must update the registration)", got.Title, "Updated Title")
	}
}

// TestPlanReplaceWithMalformedEnvelopeLeavesPriorBatchUntouched verifies that
// --replace fails without archiving the prior batch when the new envelope is
// invalid. The compile step must run BEFORE any archive/clear operations.
func TestPlanReplaceWithMalformedEnvelopeLeavesPriorBatchUntouched(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	// Establish a valid first batch.
	env1 := prd.BatchPRDEnvelope{
		Title:  "original-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-one"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-one", "Plan One")},
	}
	out1, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env1))
	if err != nil {
		t.Fatalf("initial plan: %v\n%s", err, out1)
	}
	run1 := readRunJSON(t, dir)
	batchID1 := run1.ActiveBatchID

	// Build a malformed envelope: plan with no acceptance criteria (missing AC).
	malformedEnv := prd.BatchPRDEnvelope{
		Title:  "bad-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-bad"}}},
		Plans: []prd.BatchPRDPlan{
			{
				PRD: prd.PRD{
					ID:    "plan-bad",
					Title: "Bad Plan",
					UserStories: []prd.UserStory{
						// Missing AcceptanceCriteria and Priority — should fail validation
						{ID: "US-001", Title: "Story"},
					},
				},
			},
		},
	}
	out2, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, malformedEnv), "--replace")
	if err == nil {
		t.Fatalf("expected --replace with malformed envelope to fail, got success:\n%s", out2)
	}

	// Prior batch must still exist (NOT archived).
	archivePath := filepath.Join(dir, ".springfield", "archive", batchID1+".json")
	if _, statErr := os.Stat(archivePath); statErr == nil {
		t.Errorf("prior batch %q was archived even though new envelope is invalid", batchID1)
	}

	// run.json must still point at the original batch.
	run2 := readRunJSON(t, dir)
	if run2.ActiveBatchID != batchID1 {
		t.Errorf("active_batch_id = %q after failed replace, want %q", run2.ActiveBatchID, batchID1)
	}

	// Prior batch's batch.json must still be readable.
	batchDir := filepath.Join(dir, ".springfield", "plans", batchID1)
	if _, statErr := os.Stat(filepath.Join(batchDir, "batch.json")); statErr != nil {
		t.Errorf("prior batch.json missing after failed replace: %v", statErr)
	}
}

// planUnitReg is a minimal view of a registered plan unit for test assertions.
type planUnitReg struct {
	ID    string `json:"id"`
	Order int    `json:"order"`
}

// readPlanUnits decodes the registered plan units from execution/config.json.
func readPlanUnits(t *testing.T, dir string) []planUnitReg {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".springfield", "execution", "config.json"))
	if err != nil {
		t.Fatalf("read execution config.json: %v", err)
	}
	var cfg struct {
		PlanUnits []planUnitReg `json:"plan_units"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal execution config: %v", err)
	}
	return cfg.PlanUnits
}

// TestPlanReplaceClearsStalePlanUnit is the AC happy path: a prior-batch plan
// unit P1 must be gone from the registry after --replace with new plan P2, and
// P2 must occupy order 1.
func TestPlanReplaceClearsStalePlanUnit(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	env1 := prd.BatchPRDEnvelope{
		Title:  "first-batch",
		Source: "src1",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"p1"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("p1", "Plan One")},
	}
	if out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env1)); err != nil {
		t.Fatalf("first plan: %v\n%s", err, out)
	}

	env2 := prd.BatchPRDEnvelope{
		Title:  "second-batch",
		Source: "src2",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"p2"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("p2", "Plan Two")},
	}
	if out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env2), "--replace"); err != nil {
		t.Fatalf("replace plan: %v\n%s", err, out)
	}

	units := readPlanUnits(t, dir)
	if len(units) != 1 {
		t.Fatalf("expected exactly 1 plan unit after replace, got %d: %+v", len(units), units)
	}
	if units[0].ID != "p2" {
		t.Errorf("plan unit ID = %q after replace, want p2 (stale P1 not cleared)", units[0].ID)
	}
	if units[0].Order != 1 {
		t.Errorf("p2 order = %d, want 1", units[0].Order)
	}
}

// TestPlanReplaceClearsDriftedPlanUnit covers the real-world A6 bug: the conductor
// registry has drifted from the active batch (a standalone "plans add" left an
// orphan unit holding an order slot). --replace must clear that orphan too, or the
// new envelope's unit collides with "order N already used".
func TestPlanReplaceClearsDriftedPlanUnit(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	// Active batch with one plan at order 1.
	env1 := prd.BatchPRDEnvelope{
		Title:  "first-batch",
		Source: "src1",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-one"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-one", "Plan One")},
	}
	if out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env1)); err != nil {
		t.Fatalf("first plan: %v\n%s", err, out)
	}

	// Register a standalone plan unit NOT part of the active batch. AddPlanUnit
	// stats the path, so the file must exist; it auto-assigns the next slot (2).
	staleFile := filepath.Join(dir, ".springfield", "plans", "stale.md")
	if err := os.WriteFile(staleFile, []byte("# stale plan\n"), 0o644); err != nil {
		t.Fatalf("write stale plan file: %v", err)
	}
	if out, err := runBinaryIn(t, bin, dir, "plans", "add", "--id", "stale-unit", "--path", "stale.md"); err != nil {
		t.Fatalf("plans add stale-unit: %v\n%s", err, out)
	}

	// Sanity: registry now holds both plan-one@1 and stale-unit@2.
	if got := len(readPlanUnits(t, dir)); got != 2 {
		t.Fatalf("expected 2 registered units before replace, got %d", got)
	}

	// Replace with two new plans. plan-c lands at order 2 — the slot the orphan
	// stale-unit holds. With the A6 fix the orphan is cleared first, so this
	// succeeds; without it, registration fails with "order 2 already used".
	env2 := prd.BatchPRDEnvelope{
		Title:  "second-batch",
		Source: "src2",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-b", "plan-c"}}},
		Plans: []prd.BatchPRDPlan{
			minPRDPlan("plan-b", "Plan B"),
			minPRDPlan("plan-c", "Plan C"),
		},
	}
	if out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env2), "--replace"); err != nil {
		t.Fatalf("replace plan (orphan should have been cleared): %v\n%s", err, out)
	}

	units := readPlanUnits(t, dir)
	got := make(map[string]int, len(units))
	for _, u := range units {
		got[u.ID] = u.Order
	}
	if len(got) != 2 {
		t.Fatalf("expected exactly 2 units after replace, got %d: %+v", len(got), units)
	}
	for _, stale := range []string{"plan-one", "stale-unit"} {
		if _, present := got[stale]; present {
			t.Errorf("stale unit %q still registered after replace", stale)
		}
	}
	if got["plan-b"] != 1 {
		t.Errorf("plan-b order = %d, want 1", got["plan-b"])
	}
	if got["plan-c"] != 2 {
		t.Errorf("plan-c order = %d, want 2", got["plan-c"])
	}
}

// TestPlanReplacePreservesPlanUnitsOnInvalidEnvelope is the edge case: a --replace
// that fails on an invalid new envelope must leave the prior plan units registered
// (the failure-leaves-prior-batch-untouched contract).
func TestPlanReplacePreservesPlanUnitsOnInvalidEnvelope(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	env1 := prd.BatchPRDEnvelope{
		Title:  "original-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-one"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-one", "Plan One")},
	}
	if out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env1)); err != nil {
		t.Fatalf("initial plan: %v\n%s", err, out)
	}

	// Malformed: story missing AcceptanceCriteria/Priority — fails validation.
	malformedEnv := prd.BatchPRDEnvelope{
		Title:  "bad-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-bad"}}},
		Plans: []prd.BatchPRDPlan{
			{PRD: prd.PRD{
				ID:          "plan-bad",
				Title:       "Bad Plan",
				UserStories: []prd.UserStory{{ID: "US-001", Title: "Story"}},
			}},
		},
	}
	if out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, malformedEnv), "--replace"); err == nil {
		t.Fatalf("expected --replace with malformed envelope to fail, got success:\n%s", out)
	}

	units := readPlanUnits(t, dir)
	if len(units) != 1 || units[0].ID != "plan-one" {
		t.Fatalf("prior plan unit not preserved after failed replace: %+v", units)
	}
}

// Unused import guard for fmt
var _ = fmt.Sprintf
