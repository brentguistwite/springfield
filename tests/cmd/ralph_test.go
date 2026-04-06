package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/ralph"
)

func writeRalphSpec(t *testing.T, dir string, spec ralph.Spec) string {
	t.Helper()

	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		t.Fatalf("marshal Ralph spec: %v", err)
	}

	path := filepath.Join(dir, "ralph-spec.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write Ralph spec: %v", err)
	}

	return path
}

func TestRalphInitStatusAndRun(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	specPath := writeRalphSpec(t, dir, ralph.Spec{
		Project:     "springfield",
		Description: "refresh prompt",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Bootstrap"},
			{ID: "US-002", Title: "Refresh", DependsOn: []string{"US-001"}},
		},
	})

	output, err := runBinaryIn(t, bin, dir, "ralph", "init", "--name", "refresh", "--spec", specPath)
	if err != nil {
		t.Fatalf("ralph init failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Initialized Ralph plan \"refresh\" with 2 stories.") {
		t.Fatalf("expected init output, got:\n%s", output)
	}

	output, err = runBinaryIn(t, bin, dir, "ralph", "status", "--name", "refresh")
	if err != nil {
		t.Fatalf("ralph status failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Plan: refresh") {
		t.Fatalf("expected plan name in status output, got:\n%s", output)
	}

	if !strings.Contains(output, "US-001  pending") {
		t.Fatalf("expected pending story in status output, got:\n%s", output)
	}

	output, err = runBinaryIn(t, bin, dir, "ralph", "run", "--name", "refresh")
	if err != nil {
		t.Fatalf("ralph run failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Story US-001: passed") {
		t.Fatalf("expected run output, got:\n%s", output)
	}

	output, err = runBinaryIn(t, bin, dir, "ralph", "status", "--name", "refresh")
	if err != nil {
		t.Fatalf("ralph status after run failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "US-001  passed") {
		t.Fatalf("expected updated story status, got:\n%s", output)
	}
}

func TestRalphHelpShowsRealSubcommands(t *testing.T) {
	output, err := runSpringfield(t, "ralph", "--help")
	if err != nil {
		t.Fatalf("springfield ralph --help failed: %v\n%s", err, output)
	}

	for _, marker := range []string{"init", "status", "run", "Manage Ralph plans"} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected Ralph help to mention %q, got:\n%s", marker, output)
		}
	}
}

func TestRalphRunFailsWhenNoEligibleStoriesRemain(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	specPath := writeRalphSpec(t, dir, ralph.Spec{
		Project: "springfield",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Bootstrap", Passed: true},
		},
	})

	output, err := runBinaryIn(t, bin, dir, "ralph", "init", "--name", "refresh", "--spec", specPath)
	if err != nil {
		t.Fatalf("ralph init failed: %v\n%s", err, output)
	}

	output, err = runBinaryIn(t, bin, dir, "ralph", "run", "--name", "refresh")
	if err == nil {
		t.Fatalf("expected Ralph run to fail when no stories remain, output:\n%s", output)
	}

	if !strings.Contains(output, "no eligible story") {
		t.Fatalf("expected no eligible story error, got:\n%s", output)
	}
}
