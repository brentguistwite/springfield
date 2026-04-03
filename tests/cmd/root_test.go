package cmd_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve caller for repo root")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func runSpringfield(t *testing.T, args ...string) (string, error) {
	t.Helper()

	commandArgs := append([]string{"run", "."}, args...)
	cmd := exec.Command("go", commandArgs...)
	cmd.Dir = repoRoot(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String() + stderr.String(), err
}

func TestSpringfieldHelp(t *testing.T) {
	output, err := runSpringfield(t, "--help")
	if err != nil {
		t.Fatalf("run springfield --help: %v\noutput:\n%s", err, output)
	}

	if !strings.Contains(output, "springfield") {
		t.Fatalf("expected help output to mention springfield, got:\n%s", output)
	}

	if !strings.Contains(output, "Bare springfield opens the TUI-first Springfield shell.") {
		t.Fatalf("expected help output to describe the TUI-first default, got:\n%s", output)
	}

	for _, subcommand := range []string{"ralph", "conductor", "doctor"} {
		if !strings.Contains(output, subcommand) {
			t.Fatalf("expected help output to mention %q, got:\n%s", subcommand, output)
		}
	}
}

func TestSpringfieldWithoutArgsShowsPlaceholder(t *testing.T) {
	output, err := runSpringfield(t)
	if err != nil {
		t.Fatalf("run springfield: %v\noutput:\n%s", err, output)
	}

	if !strings.Contains(output, "Springfield TUI Placeholder") {
		t.Fatalf("expected placeholder output, got:\n%s", output)
	}

	if strings.Contains(output, "Usage:") {
		t.Fatalf("expected bare springfield to avoid a plain help dump, got:\n%s", output)
	}
}

func TestSpringfieldSubcommandsAreReachable(t *testing.T) {
	for _, subcommand := range []struct {
		name   string
		marker string
	}{
		{name: "ralph", marker: "Ralph workflows will move behind Springfield."},
		{name: "conductor", marker: "Conductor workflows will move behind Springfield."},
		{name: "doctor", marker: "Doctor will check local Springfield setup."},
	} {
		output, err := runSpringfield(t, subcommand.name, "--help")
		if err != nil {
			t.Fatalf("run springfield %s --help: %v\noutput:\n%s", subcommand.name, err, output)
		}

		if !strings.Contains(output, subcommand.marker) {
			t.Fatalf("expected %s help output to contain %q, got:\n%s", subcommand.name, subcommand.marker, output)
		}
	}
}
