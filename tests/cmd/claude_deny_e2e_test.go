package cmd_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestSpringfieldStartPlumbsControlPlaneHookToClaude verifies the PreToolUse
// hook JSON reaches the invoked `claude` binary's argv at runtime (not just
// the adapter unit path), so regressions in the runtime request plumbing are
// caught too.
func TestSpringfieldStartPlumbsControlPlaneHookToClaude(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}

	fakeBinDir := filepath.Join(dir, "bin")
	argvPath := filepath.Join(dir, "claude.argv")
	installFakeAgentBinary(t, fakeBinDir, "claude", argvPath)

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir}, "start")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, output)
	}

	argv := readRecordedArgs(t, argvPath)
	settings := ""
	for i, a := range argv {
		if a == "--settings" && i+1 < len(argv) {
			settings = argv[i+1]
			break
		}
	}
	if settings == "" {
		t.Fatalf("expected --settings arg in recorded argv, got %v", argv)
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(settings), &raw); err != nil {
		t.Fatalf("parse --settings JSON: %v (raw=%q)", err, settings)
	}

	if _, ok := raw["permissions"]; ok {
		t.Fatalf("expected no permissions block in settings, got %q", settings)
	}

	hooks, ok := raw["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("expected hooks block in settings, got %q", settings)
	}
	preList, ok := hooks["PreToolUse"].([]any)
	if !ok || len(preList) == 0 {
		t.Fatalf("expected PreToolUse list, got %v", hooks)
	}
	first, ok := preList[0].(map[string]any)
	if !ok {
		t.Fatalf("expected PreToolUse[0] map, got %T", preList[0])
	}
	if got, want := first["matcher"], "Write|Edit|MultiEdit|NotebookEdit|Bash"; got != want {
		t.Fatalf("matcher = %v, want %q", got, want)
	}
	inner, ok := first["hooks"].([]any)
	if !ok || len(inner) == 0 {
		t.Fatalf("expected inner hooks list, got %v", first)
	}
	innerFirst, ok := inner[0].(map[string]any)
	if !ok {
		t.Fatalf("expected inner hooks[0] map, got %T", inner[0])
	}
	if got, want := innerFirst["type"], "command"; got != want {
		t.Fatalf("hook type = %v, want %q", got, want)
	}
	cmdStr, ok := innerFirst["command"].(string)
	if !ok {
		t.Fatalf("expected hook command string, got %T", innerFirst["command"])
	}
	if !strings.Contains(cmdStr, ".springfield") {
		t.Fatalf("hook command missing .springfield substring check: %q", cmdStr)
	}
	if !strings.Contains(cmdStr, "off-limits") {
		t.Fatalf("hook command missing off-limits message: %q", cmdStr)
	}
}
