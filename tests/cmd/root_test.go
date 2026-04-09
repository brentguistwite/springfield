package cmd_test

import (
	"bytes"
	"fmt"
	"os"
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

func runBinaryInWithEnv(t *testing.T, bin, dir string, env []string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = mergeEnv(os.Environ(), env)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String() + stderr.String(), err
}

func mergeEnv(base, overrides []string) []string {
	merged := make([]string, 0, len(base)+len(overrides))
	skip := make(map[string]bool, len(overrides))
	for _, entry := range overrides {
		if idx := strings.IndexByte(entry, '='); idx > 0 {
			skip[entry[:idx]] = true
		}
	}

	for _, entry := range base {
		if idx := strings.IndexByte(entry, '='); idx > 0 && skip[entry[:idx]] {
			continue
		}
		merged = append(merged, entry)
	}

	merged = append(merged, overrides...)
	return merged
}

func installFakeAgentBinary(t *testing.T, binDir, name, argvPath string) {
	t.Helper()

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bin dir: %v", err)
	}

	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\necho 'agent-output'\n", argvPath)
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s binary: %v", name, err)
	}
}

func readRecordedArgs(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recorded args: %v", err)
	}

	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}

	return strings.Split(text, "\n")
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
	if strings.Contains(output, "springfield conductor setup") {
		t.Errorf("init should not direct users to the conductor surface, got:\n%s", output)
	}
	if !strings.Contains(output, `Next: run "springfield" to continue in guided setup.`) {
		t.Errorf("expected Springfield-only next step, got:\n%s", output)
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

	if strings.Contains(output, "springfield ralph") {
		t.Fatalf("help should not advertise legacy ralph surface, got:\n%s", output)
	}
	if strings.Contains(output, "springfield conductor") {
		t.Fatalf("help should not advertise legacy conductor surface, got:\n%s", output)
	}
	if !strings.Contains(output, "Springfield is the local-first CLI and TUI entrypoint") {
		t.Fatalf("expected Springfield-first help text, got:\n%s", output)
	}
	if !strings.Contains(output, "doctor") {
		t.Fatalf("expected help output to mention doctor, got:\n%s", output)
	}
	for _, name := range []string{"status", "resume", "diagnose"} {
		if !strings.Contains(output, name) {
			t.Fatalf("expected help output to mention %s, got:\n%s", name, output)
		}
	}
}

func TestSpringfieldHelpReadmeStaysSpringfieldFirst(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	text := string(data)
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"planning is real end-to-end",
		"execution is unified behind Springfield-owned run, status, resume, and diagnose surfaces",
	} {
		if !strings.Contains(lower, strings.ToLower(marker)) {
			t.Fatalf("expected README to contain %q, got:\n%s", marker, text)
		}
	}
	for _, stale := range []string{
		"springfield conductor setup",
		"go run . ralph --help",
	} {
		if strings.Contains(text, stale) {
			t.Fatalf("expected README to drop stale guidance %q, got:\n%s", stale, text)
		}
	}
}

func TestSpringfieldWithoutArgsShowsShellHome(t *testing.T) {
	output, err := runSpringfield(t)
	if err != nil {
		t.Fatalf("run springfield: %v\noutput:\n%s", err, output)
	}

	if !strings.Contains(output, "Local-first shell for planning and running work.") {
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
		{name: "explain", marker: "Render the built-in Springfield explanation prompt for the current project."},
		{name: "status", marker: "Show status for the active Springfield work or a specific work id."},
		{name: "resume", marker: "Run or resume the active approved Springfield work."},
		{name: "diagnose", marker: "Summarize Springfield failures, evidence, and next steps for the active work."},
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

func TestExplainRendersPlaybookBackedOutput(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("project context from AGENTS"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "explain")
	if err != nil {
		t.Fatalf("run springfield explain: %v\noutput:\n%s", err, output)
	}

	if !strings.Contains(output, "# Springfield Playbook") {
		t.Fatalf("expected explain output to render playbook prompt, got:\n%s", output)
	}
	if !strings.Contains(output, "project context from AGENTS") {
		t.Fatalf("expected explain output to include project context, got:\n%s", output)
	}
}

func TestExplainPrefersAGENTSContext(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("AGENTS wins"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("CLAUDE fallback"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "explain")
	if err != nil {
		t.Fatalf("run springfield explain: %v\noutput:\n%s", err, output)
	}

	if !strings.Contains(output, "AGENTS wins") {
		t.Fatalf("expected AGENTS context in explain output, got:\n%s", output)
	}
	if strings.Contains(output, "CLAUDE fallback") {
		t.Fatalf("expected AGENTS to take precedence over CLAUDE, got:\n%s", output)
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
