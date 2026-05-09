package cmd_test

import (
	"strings"
	"testing"
)

func TestSpringfieldPlanHelp(t *testing.T) {
	output, err := runSpringfield(t, "plan", "--help")
	if err != nil {
		t.Fatalf("plan --help failed: %v\n%s", err, output)
	}

	for _, marker := range []string{
		"Compile a Springfield plan from a caller-provided slice payload.",
		"--slices",
		"--replace",
		"--append",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected plan help to contain %q, got:\n%s", marker, output)
		}
	}
}

func TestSpringfieldPlanFromSlicesStdin(t *testing.T) {
	t.Skip("TODO(phase-3) batch ingest pending PRD rewrite")
}

func TestSpringfieldPlanFromSlicesFile(t *testing.T) {
	t.Skip("TODO(phase-3) batch ingest pending PRD rewrite")
}

func TestSpringfieldPlanRequiresSlicesFlag(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	output, err := runBinaryIn(t, bin, dir, "plan")
	if err == nil {
		t.Fatalf("expected error when --slices missing, got:\n%s", output)
	}
	if !strings.Contains(output, "--slices is required") {
		t.Fatalf("expected '--slices is required' error, got:\n%s", output)
	}
}

func TestSpringfieldPlanRejectsInvalidPayload(t *testing.T) {
	t.Skip("TODO(phase-3) batch ingest pending PRD rewrite")
}

func TestSpringfieldPlanRefusesWithActiveBatch(t *testing.T) {
	t.Skip("TODO(phase-3) batch ingest pending PRD rewrite")
}

func TestSpringfieldPlanReplaceArchivesPrior(t *testing.T) {
	t.Skip("TODO(phase-3) batch ingest pending PRD rewrite")
}

func TestSpringfieldPlanReplaceKeepsPriorWhenNewFails(t *testing.T) {
	t.Skip("TODO(phase-3) batch ingest pending PRD rewrite")
}

func TestSpringfieldPlanAppendDedupsSliceIDs(t *testing.T) {
	t.Skip("TODO(phase-3) batch ingest pending PRD rewrite")
}

func TestSpringfieldPlanUnsafeIDSanitized(t *testing.T) {
	t.Skip("TODO(phase-3) batch ingest pending PRD rewrite")
}
