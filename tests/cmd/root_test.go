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

func buildBinary(t *testing.T) string {
	t.Helper()

	return buildBinaryWithFlags(t)
}

func buildBinaryWithFlags(t *testing.T, extraArgs ...string) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "springfield")
	args := append([]string{"build"}, extraArgs...)
	args = append(args, "-o", bin, ".")
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot(t)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build binary: %v\n%s", err, out)
	}
	return bin
}

func runBinaryIn(t *testing.T, bin, dir string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.Command(bin, args...)
	cmd.Dir = dir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String() + stderr.String(), err
}

func runBinaryInWithInput(t *testing.T, bin, dir, input string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String() + stderr.String(), err
}

func TestInitCreatesProjectInCurrentDir(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(t, bin, dir, "init")
	if err != nil {
		t.Fatalf("springfield init failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Created springfield.toml") {
		t.Errorf("expected creation message, got:\n%s", output)
	}
	if !strings.Contains(output, "Created .springfield/") {
		t.Errorf("expected runtime dir message, got:\n%s", output)
	}

	// Re-run should show skip messages
	output2, err := runBinaryIn(t, bin, dir, "init")
	if err != nil {
		t.Fatalf("re-run init failed: %v\n%s", err, output2)
	}
	if !strings.Contains(output2, "already exists") {
		t.Errorf("expected skip messages on re-run, got:\n%s", output2)
	}
}

func TestInitAppearsInHelp(t *testing.T) {
	output, err := runSpringfield(t, "--help")
	if err != nil {
		t.Fatalf("help failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "init") {
		t.Errorf("expected init in help output, got:\n%s", output)
	}
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

func TestSpringfieldWithoutArgsShowsShellHome(t *testing.T) {
	output, err := runSpringfield(t)
	if err != nil {
		t.Fatalf("run springfield: %v\noutput:\n%s", err, output)
	}

	if !strings.Contains(output, "Local-first shell for Ralph and Conductor.") {
		t.Fatalf("expected shell home output, got:\n%s", output)
	}

	if !strings.Contains(output, "Guided Setup") {
		t.Fatalf("expected Guided Setup in shell home output, got:\n%s", output)
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
		{name: "init", marker: "Initialize a new Springfield project in the current directory."},
		{name: "ralph", marker: "Manage Ralph plans, story selection, and local run history."},
		{name: "conductor", marker: "Orchestrate plan execution, check status, resume from failures, and diagnose issues."},
		{name: "doctor", marker: "Doctor checks that supported agent CLIs are installed and reachable, providing install guidance for anything missing."},
		{name: "version", marker: "Print the Springfield version"},
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

func TestVersionDefaultsToDev(t *testing.T) {
	bin := buildBinary(t)

	output, err := runBinaryIn(t, bin, t.TempDir(), "version")
	if err != nil {
		t.Fatalf("run springfield version: %v\noutput:\n%s", err, output)
	}

	if strings.TrimSpace(output) != "springfield dev" {
		t.Fatalf("expected default dev version, got:\n%s", output)
	}
}

func TestVersionUsesBuildTimeOverride(t *testing.T) {
	bin := buildBinaryWithFlags(t, "-ldflags", "-X springfield/cmd.Version=v1.2.3")

	output, err := runBinaryIn(t, bin, t.TempDir(), "version")
	if err != nil {
		t.Fatalf("run springfield version with ldflags: %v\noutput:\n%s", err, output)
	}

	if strings.TrimSpace(output) != "springfield v1.2.3" {
		t.Fatalf("expected ldflags version override, got:\n%s", output)
	}
}
