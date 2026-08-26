package cmd_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"springfield/internal/features/prd"
)

// singleSlicePlan compiles a minimal single-plan PRD envelope with the given
// title (used as both plan source and title), then calls planWithPRD to run
// "springfield plan --prd -". Returns combined stdout+stderr and error.
//
// The batch ID is derived from the title (slugified). The plan ID is "plan-1"
// so it sorts after the batch dir, letting findPlanDir return the batch dir.
func singleSlicePlan(t *testing.T, bin, dir, title string, extraArgs ...string) (string, error) {
	t.Helper()
	env := prd.BatchPRDEnvelope{
		Title:  title,
		Source: title,
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-1"}}},
		Plans: []prd.BatchPRDPlan{
			{
				PRD: prd.PRD{
					ID:    "plan-1",
					Title: title,
					UserStories: []prd.UserStory{{
						ID:                 "US-001",
						Title:              title,
						Description:        title,
						AcceptanceCriteria: []string{"passes"},
						Priority:           1,
					}},
				},
			},
		},
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("marshal singleSlicePlan envelope: %v", err)
	}
	return planWithPRD(t, bin, dir, string(data), extraArgs...)
}

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

	// Emit a positive-signal stream-json line (tool_use + non-error
	// tool_result collapsed into one assistant message) so the
	// ValidateResult contract treats this no-op agent as a successful
	// run. Without this, the strict positive-signal contract would
	// reject the bare "agent-output" stdout as "no tool invoked."
	const positiveSignalLine = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_fake"},{"type":"tool_result","tool_use_id":"toolu_fake","is_error":false}]}}`
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\necho '%s'\necho 'agent-output'\n", argvPath, positiveSignalLine)
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s binary: %v", name, err)
	}
}

// installPRDFakeAgentBinary installs a fake agent that emits PRD story-pass
// and COMPLETE markers so the PRD iteration loop in planrun exits cleanly.
// storyIDs lists the US-NNN ids to mark passed (e.g. "US-001").
// It also emits a positive-signal JSON so ValidateResult passes.
func installPRDFakeAgentBinary(t *testing.T, binDir, name string, storyIDs []string) {
	t.Helper()

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bin dir: %v", err)
	}

	const positiveSignalLine = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_fake"},{"type":"tool_result","tool_use_id":"toolu_fake","is_error":false}]}}`
	markers := ""
	for _, id := range storyIDs {
		markers += "<story-pass>" + id + "</story-pass>"
	}
	markers += "<promise>COMPLETE</promise>"
	// The script configures git so commits work inside worktrees, makes a
	// trivial commit so the merge phase has something to merge, then emits
	// the markers that the PRD iteration loop requires.
	script := "#!/bin/sh\n" +
		"git config user.email agent@example.com 2>/dev/null || true\n" +
		"git config user.name Agent 2>/dev/null || true\n" +
		"echo 'done' > agent-work.txt\n" +
		"git add agent-work.txt 2>/dev/null || true\n" +
		"git commit -m 'agent work' >/dev/null 2>&1 || true\n" +
		"echo '" + positiveSignalLine + "'\n" +
		"echo '" + markers + "'\n"
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write PRD fake %s binary: %v", name, err)
	}
}

func installFailingAgentBinary(t *testing.T, binDir, name string) {
	t.Helper()

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bin dir: %v", err)
	}

	script := "#!/bin/sh\necho 'agent stderr line' 1>&2\nexit 1\n"
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write failing %s binary: %v", name, err)
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

func availableCommands(help string) []string {
	lines := strings.Split(help, "\n")
	commands := make([]string, 0)
	inCommands := false

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "Available Commands:"):
			inCommands = true
			continue
		case inCommands && strings.TrimSpace(line) == "":
			inCommands = false
			continue
		case inCommands && strings.HasPrefix(line, "Flags:"):
			inCommands = false
			continue
		case inCommands:
			fields := strings.Fields(line)
			if len(fields) > 0 {
				commands = append(commands, fields[0])
			}
		}
	}

	slices.Sort(commands)
	return commands
}

func TestInitCreatesProjectInCurrentDir(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex")
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
	if !strings.Contains(output, "/springfield:plan") {
		t.Errorf("expected post-init Next: line pointing at the plan skill, got:\n%s", output)
	}
	if !strings.Contains(output, "Then: springfield start") {
		t.Errorf("expected post-init Then: line pointing at springfield start, got:\n%s", output)
	}
	for _, stale := range []string{"marketplace", "Codex plugin/catalog", "springfield install"} {
		if strings.Contains(output, stale) {
			t.Errorf("post-init copy still mentions stale install pitch %q:\n%s", stale, output)
		}
	}

	// Re-run should show skip messages
	output2, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex")
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
	if strings.Contains(output, "internal-debug") {
		t.Fatalf("help should not advertise hidden debug surface, got:\n%s", output)
	}
	if !strings.Contains(output, "Springfield is plugin-first") {
		t.Fatalf("expected Springfield-first help text, got:\n%s", output)
	}

	if got, want := availableCommands(output), []string{"batch", "doctor", "init", "install", "plan", "plans", "recover", "start", "status", "version"}; !slices.Equal(got, want) {
		t.Fatalf("available commands = %v, want %v\nfull output:\n%s", got, want, output)
	}
}

func TestSpringfieldBareShowsInstallGuidance(t *testing.T) {
	output, err := runSpringfield(t)
	if err != nil {
		t.Fatalf("run bare springfield: %v\noutput:\n%s", err, output)
	}

	for _, marker := range []string{
		"Primary CLI install: brew install brentguistwite/tap/springfield",
		"springfield install",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected bare springfield output to contain %q, got:\n%s", marker, output)
		}
	}
	if strings.Contains(output, "Guided Setup") {
		t.Fatalf("bare springfield should not include retired interactive-shell guidance, got:\n%s", output)
	}
}

func TestSpringfieldWithoutArgsShowsHelpAndGuidance(t *testing.T) {
	output, err := runSpringfield(t)
	if err != nil {
		t.Fatalf("run springfield: %v\noutput:\n%s", err, output)
	}

	for _, marker := range []string{
		"Usage:",
		"springfield install",
		"Primary CLI install: brew install brentguistwite/tap/springfield",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected bare springfield output to contain %q, got:\n%s", marker, output)
		}
	}
	if strings.Contains(output, "Guided Setup") {
		t.Fatalf("expected bare springfield to avoid TUI shell output, got:\n%s", output)
	}
}

func TestSpringfieldPublicSubcommandsAreReachable(t *testing.T) {
	for _, subcommand := range []struct {
		name   string
		marker string
	}{
		{name: "init", marker: "Initialize a new Springfield project in the current directory."},
		{name: "install", marker: "Sync Springfield local host artifacts for Claude Code and Codex."},
		{name: "status", marker: "Show status for the active Springfield batch."},
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

func TestInternalDebugCommandIsRemoved(t *testing.T) {
	output, err := runSpringfield(t, "internal-debug", "--help")
	if err == nil {
		t.Fatalf("expected internal-debug command removal, got successful output:\n%s", output)
	}
	if !strings.Contains(output, "unknown command") {
		t.Fatalf("expected unknown command output, got:\n%s", output)
	}
}

func TestOnlyPublicTopLevelCommandsRemainReachable(t *testing.T) {
	for _, subcommand := range []string{"explain", "skills", "internal-debug"} {
		output, err := runSpringfield(t, subcommand, "--help")
		if err == nil {
			t.Fatalf("expected %s command removal, got successful output:\n%s", subcommand, output)
		}
		if !strings.Contains(output, "unknown command") {
			t.Fatalf("expected unknown command output for %s, got:\n%s", subcommand, output)
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
