package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/conductor"
)

func writeConductorConfigBinary(t *testing.T, root string, cfg *conductor.Config) {
	t.Helper()

	dir := filepath.Join(root, ".springfield", "conductor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir conductor: %v", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal conductor config: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644); err != nil {
		t.Fatalf("write conductor config: %v", err)
	}
}

func writePlanFileBinary(t *testing.T, root, plansDir, name, content string) {
	t.Helper()

	dir := filepath.Join(root, plansDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write plan file: %v", err)
	}
}

func TestConductorRunReportsTruthfulFailure(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	writeSpringfieldConfig(t, dir, "claude")

	cfg := &conductor.Config{
		PlansDir:        ".conductor/plans",
		WorktreeBase:    ".worktrees",
		MaxRetries:      1,
		RalphIterations: 1,
		RalphTimeout:    10,
		Tool:            "claude",
		Sequential:      []string{"01-bootstrap"},
	}
	writeConductorConfigBinary(t, dir, cfg)
	writePlanFileBinary(t, dir, ".conductor/plans", "01-bootstrap", "implement bootstrap")

	// Real runner will fail because no agent binary exists in CI.
	// The output must truthfully report the failure.
	output, err := runBinaryIn(t, bin, dir, "conductor", "run")

	// Should return an error (non-zero exit) because execution failed
	if err == nil {
		t.Fatalf("expected conductor run to return error when agent is missing, output:\n%s", output)
	}

	if !strings.Contains(output, "failed") {
		t.Fatalf("expected truthful failure status, got:\n%s", output)
	}

	if !strings.Contains(output, "01-bootstrap") {
		t.Fatalf("expected failed plan name in output, got:\n%s", output)
	}

	// Must NOT claim success
	if strings.Contains(output, "Completed") && strings.Contains(output, "successfully") {
		t.Fatalf("should not claim success when execution failed, got:\n%s", output)
	}
}

func TestConductorRunDryRunShowsPlans(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	writeSpringfieldConfig(t, dir, "claude")

	cfg := &conductor.Config{
		PlansDir:     ".conductor/plans",
		WorktreeBase: ".worktrees",
		Tool:         "claude",
		Sequential:   []string{"01-bootstrap", "02-config"},
	}
	writeConductorConfigBinary(t, dir, cfg)

	output, err := runBinaryIn(t, bin, dir, "conductor", "run", "--dry-run")
	if err != nil {
		t.Fatalf("conductor run --dry-run failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "01-bootstrap") {
		t.Fatalf("expected first plan in dry-run output, got:\n%s", output)
	}

	if !strings.Contains(output, "0/2") {
		t.Fatalf("expected progress count in dry-run output, got:\n%s", output)
	}
}

func TestConductorStatusAfterFailedRun(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	writeSpringfieldConfig(t, dir, "claude")

	cfg := &conductor.Config{
		PlansDir:        ".conductor/plans",
		WorktreeBase:    ".worktrees",
		MaxRetries:      1,
		RalphIterations: 1,
		RalphTimeout:    10,
		Tool:            "claude",
		Sequential:      []string{"01-bootstrap", "02-config"},
	}
	writeConductorConfigBinary(t, dir, cfg)
	writePlanFileBinary(t, dir, ".conductor/plans", "01-bootstrap", "implement bootstrap")
	writePlanFileBinary(t, dir, ".conductor/plans", "02-config", "implement config")

	// Run conductor (will fail at first plan due to no agent binary)
	runBinaryIn(t, bin, dir, "conductor", "run")

	// Status should reflect the failed state truthfully
	output, err := runBinaryIn(t, bin, dir, "conductor", "status")
	if err != nil {
		t.Fatalf("conductor status failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "01-bootstrap") {
		t.Fatalf("expected plan name in status, got:\n%s", output)
	}

	// First plan should be failed, second should still be pending
	if !strings.Contains(output, "failed") {
		t.Fatalf("expected failed status for first plan, got:\n%s", output)
	}

	if !strings.Contains(output, "pending") {
		t.Fatalf("expected pending status for second plan, got:\n%s", output)
	}
}

func TestConductorDiagnoseAfterFailedRun(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	writeSpringfieldConfig(t, dir, "claude")

	cfg := &conductor.Config{
		PlansDir:        ".conductor/plans",
		WorktreeBase:    ".worktrees",
		MaxRetries:      1,
		RalphIterations: 1,
		RalphTimeout:    10,
		Tool:            "claude",
		Sequential:      []string{"01-bootstrap"},
	}
	writeConductorConfigBinary(t, dir, cfg)
	writePlanFileBinary(t, dir, ".conductor/plans", "01-bootstrap", "implement bootstrap")

	// Run conductor (will fail)
	runBinaryIn(t, bin, dir, "conductor", "run")

	// Diagnose should report the failure with actionable info
	output, err := runBinaryIn(t, bin, dir, "conductor", "diagnose")
	if err != nil {
		t.Fatalf("conductor diagnose failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "01-bootstrap") {
		t.Fatalf("expected failed plan in diagnose output, got:\n%s", output)
	}

	if !strings.Contains(output, "resume") {
		t.Fatalf("expected resume guidance in diagnose output, got:\n%s", output)
	}
}
