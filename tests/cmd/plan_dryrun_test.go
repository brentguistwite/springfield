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
