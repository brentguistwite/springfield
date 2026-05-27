package plugin_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHooksJSONBlocksControlPlane verifies the plugin ships a PreToolUse hook
// that routes Write/Edit-family tool calls through `springfield hook-guard`,
// which denies any interactive write to the .springfield/ control plane. The
// session-start binary-fetch hook (and its checksum manifest) was removed in
// favor of manual CLI install, so a SessionStart entry must be absent.
func TestHooksJSONBlocksControlPlane(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := doc.Hooks["SessionStart"]; ok {
		t.Fatal("hooks.json must not ship a SessionStart hook (manual CLI install replaced the binary fetch)")
	}

	pre, ok := doc.Hooks["PreToolUse"]
	if !ok || len(pre) == 0 {
		t.Fatal("hooks.json missing PreToolUse guard")
	}
	entry := pre[0]
	for _, tool := range []string{"Write", "Edit"} {
		if !strings.Contains(entry.Matcher, tool) {
			t.Errorf("PreToolUse matcher %q should include %q", entry.Matcher, tool)
		}
	}
	if len(entry.Hooks) == 0 {
		t.Fatal("PreToolUse[0].hooks empty")
	}
	h := entry.Hooks[0]
	if h.Type != "command" {
		t.Errorf("hook type = %q, want command", h.Type)
	}
	if !strings.Contains(h.Command, "hook-guard") {
		t.Errorf("hook command = %q, want it to invoke `springfield hook-guard`", h.Command)
	}
}

// TestHooksDirHasNoSessionStartArtifacts guards the tear-down: the binary-fetch
// shell script and its checksum manifest must be gone.
func TestHooksDirHasNoSessionStartArtifacts(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{"hooks/session-start.sh", "hooks/checksums.txt"} {
		if _, err := os.Stat(filepath.Join(root, rel)); !os.IsNotExist(err) {
			t.Errorf("%s should be removed, stat err = %v", rel, err)
		}
	}
}
