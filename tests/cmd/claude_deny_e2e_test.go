package cmd_test

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// TestSpringfieldStartPlumbsDenySettingsToClaude verifies the deny-rules JSON
// reaches the invoked `claude` binary's argv at runtime (not just the adapter
// unit path), so regressions in the runtime request plumbing are caught too.
func TestSpringfieldStartPlumbsDenySettingsToClaude(t *testing.T) {
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

	var parsed struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		t.Fatalf("parse --settings JSON: %v (raw=%q)", err, settings)
	}
	for _, rule := range []string{
		"Write(.springfield/**)",
		"Edit(.springfield/**)",
		"Bash(* .springfield/**)",
	} {
		found := false
		for _, d := range parsed.Permissions.Deny {
			if d == rule {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected deny rule %q in %v", rule, parsed.Permissions.Deny)
		}
	}
}
