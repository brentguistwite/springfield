package cmd_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/prd"
)

// fingerprintSpringfieldDir captures a deterministic snapshot of every file
// under .springfield/ for byte-identical pre/post comparison.
// Returns nil when the directory does not exist.
func fingerprintSpringfieldDir(t *testing.T, root string) map[string]string {
	t.Helper()
	sf := filepath.Join(root, ".springfield")
	info, err := os.Stat(sf)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("stat .springfield: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf(".springfield is not a directory")
	}
	out := make(map[string]string)
	err = filepath.Walk(sf, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		sum := sha256.Sum256(data)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("walk .springfield: %v", err)
	}
	return out
}

func mapEqual(a, b map[string]string) (string, bool) {
	if len(a) != len(b) {
		return "size differs", false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			return "missing " + k, false
		}
		if va != vb {
			return "diff " + k, false
		}
	}
	return "", true
}

func TestPlanDryRunFreshProjectWritesNothing(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	pre := fingerprintSpringfieldDir(t, dir)

	env := prd.BatchPRDEnvelope{
		Title:  "preview-batch",
		Source: "test-source",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"preview-01"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("preview-01", "Preview 01")},
	}
	out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run plan: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Dry run: would compile batch") {
		t.Fatalf("expected dry-run summary:\n%s", out)
	}
	if !strings.Contains(out, "preview-01") {
		t.Fatalf("expected plan id in summary:\n%s", out)
	}

	post := fingerprintSpringfieldDir(t, dir)
	if diff, eq := mapEqual(pre, post); !eq {
		t.Fatalf(".springfield/ mutated by --dry-run: %s", diff)
	}
}

// TestPlanDryRunToleratesMissingExecutionConfig pins the dry-run no-write
// contract against a project that has springfield.toml but no
// execution/config.json (the state a pre-bootstrap init left behind). Dry-run
// must not crash on the missing config, must not create it (zero writes under
// .springfield/), and must still produce a compile preview.
func TestPlanDryRunToleratesMissingExecutionConfig(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	// Only the TOML — deliberately no execution/config.json.
	writeSpringfieldConfig(t, dir, "claude")

	pre := fingerprintSpringfieldDir(t, dir)

	out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, minEnvelope("preview-01", "Preview 01")), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run with missing execution config should succeed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Dry run: would compile batch") {
		t.Fatalf("expected dry-run summary:\n%s", out)
	}

	post := fingerprintSpringfieldDir(t, dir)
	if diff, eq := mapEqual(pre, post); !eq {
		t.Fatalf(".springfield/ mutated by --dry-run with missing config: %s", diff)
	}
}

// TestPlanDryRunPlainModeWithActiveBatchWarns confirms the operator-visible
// warning when --dry-run is invoked against a project with an active batch
// but no --replace/--append flag.
func TestPlanDryRunPlainModeWithActiveBatchWarns(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	// Seed an active batch via a real plan invocation.
	env1 := prd.BatchPRDEnvelope{
		Title:  "existing-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"existing-01"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("existing-01", "Existing 01")},
	}
	if out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env1)); err != nil {
		t.Fatalf("seed plan: %v\n%s", err, out)
	}

	pre := fingerprintSpringfieldDir(t, dir)

	env2 := prd.BatchPRDEnvelope{
		Title:  "preview-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"preview-01"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("preview-01", "Preview 01")},
	}
	stdout, stderr, err := runPlanSplit(t, bin, dir, buildEnvelopeJSON(t, env2), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run plain mode against active batch should succeed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stderr, "[warn] active batch") {
		t.Fatalf("expected stderr warning that active batch will not be modified:\nstderr:\n%s", stderr)
	}
	if !strings.Contains(stdout, "Dry run: would compile batch") {
		t.Fatalf("expected dry-run summary on stdout:\nstdout:\n%s", stdout)
	}

	post := fingerprintSpringfieldDir(t, dir)
	if diff, eq := mapEqual(pre, post); !eq {
		t.Fatalf(".springfield/ mutated by plain --dry-run with active batch: %s", diff)
	}
}

func TestPlanDryRunReplaceLeavesActiveBatchUntouched(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	// First, plan a real batch.
	env1 := prd.BatchPRDEnvelope{
		Title:  "first-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"first-01"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("first-01", "First 01")},
	}
	out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env1))
	if err != nil {
		t.Fatalf("initial plan: %v\n%s", err, out)
	}

	pre := fingerprintSpringfieldDir(t, dir)

	// Now dry-run --replace.
	env2 := prd.BatchPRDEnvelope{
		Title:  "replacement-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"replacement-01"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("replacement-01", "Replacement 01")},
	}
	out, err = planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env2), "--replace", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run replace: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Dry run: would compile batch") {
		t.Fatalf("expected dry-run summary:\n%s", out)
	}

	post := fingerprintSpringfieldDir(t, dir)
	if diff, eq := mapEqual(pre, post); !eq {
		t.Fatalf(".springfield/ mutated by --replace --dry-run: %s", diff)
	}
}

// TestPlanDryRunAppendAgainstOrphanRunSucceeds exercises the path where
// run.json points at a batch whose batch.json was deleted (orphan state).
// In that case the missing-batch error must be swallowed so the dry-run
// proceeds with an empty existing-ID set. Regression test for the
// err-vs-berr variable shadowing bug.
func TestPlanDryRunAppendAgainstOrphanRunSucceeds(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	// Manufacture an orphan run by writing run.json but never producing a batch.json.
	if err := os.WriteFile(
		filepath.Join(dir, ".springfield", "run.json"),
		[]byte(`{"active_batch_id":"ghost"}`),
		0o644,
	); err != nil {
		t.Fatalf("write orphan run.json: %v", err)
	}

	pre := fingerprintSpringfieldDir(t, dir)

	env := prd.BatchPRDEnvelope{
		Title:  "append-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"new-plan"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("new-plan", "New Plan")},
	}
	out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env), "--append", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run append against orphan should succeed (missing batch.json swallowed): %v\n%s", err, out)
	}
	if !strings.Contains(out, "Dry run: would compile batch") {
		t.Fatalf("expected dry-run summary even with orphan run.json:\n%s", out)
	}

	post := fingerprintSpringfieldDir(t, dir)
	if diff, eq := mapEqual(pre, post); !eq {
		t.Fatalf(".springfield/ mutated by --append --dry-run on orphan: %s", diff)
	}
}

func TestPlanDryRunAppendCollisionErrorsWithoutMutation(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	env1 := prd.BatchPRDEnvelope{
		Title:  "base-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-x"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-x", "Plan X")},
	}
	out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env1))
	if err != nil {
		t.Fatalf("initial plan: %v\n%s", err, out)
	}

	pre := fingerprintSpringfieldDir(t, dir)

	// Append a colliding id.
	env2 := prd.BatchPRDEnvelope{
		Title:  "append-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-x"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-x", "Plan X duplicate")},
	}
	out, err = planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env2), "--append", "--dry-run")
	if err == nil {
		t.Fatalf("expected collision error, got:\n%s", out)
	}
	if !strings.Contains(out, "already exists") {
		t.Fatalf("expected 'already exists' in error:\n%s", out)
	}

	post := fingerprintSpringfieldDir(t, dir)
	if diff, eq := mapEqual(pre, post); !eq {
		t.Fatalf(".springfield/ mutated by --append --dry-run with collision: %s", diff)
	}
}
