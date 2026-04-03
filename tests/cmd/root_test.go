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
}

func TestSpringfieldWithoutArgsShowsUsage(t *testing.T) {
	output, err := runSpringfield(t)
	if err != nil {
		t.Fatalf("run springfield: %v\noutput:\n%s", err, output)
	}

	if !strings.Contains(output, "Usage:") {
		t.Fatalf("expected usage output, got:\n%s", output)
	}
}
