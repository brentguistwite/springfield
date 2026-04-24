package plugin_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestHooksJSONShape(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var doc struct {
		Description string `json:"description"`
		Hooks       struct {
			SessionStart []struct {
				Hooks []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"SessionStart"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Hooks.SessionStart) == 0 {
		t.Fatal("SessionStart hook missing")
	}
	if len(doc.Hooks.SessionStart[0].Hooks) == 0 {
		t.Fatal("SessionStart[0].hooks empty")
	}
	if doc.Hooks.SessionStart[0].Hooks[0].Type != "command" {
		t.Fatalf("type = %q, want command", doc.Hooks.SessionStart[0].Hooks[0].Type)
	}
	cmd := doc.Hooks.SessionStart[0].Hooks[0].Command
	want := "${CLAUDE_PLUGIN_ROOT}/hooks/session-start.sh"
	if cmd != want {
		t.Fatalf("command = %q, want %q", cmd, want)
	}
}

func TestSessionStartScriptExecutable(t *testing.T) {
	root := repoRoot(t)
	info, err := os.Stat(filepath.Join(root, "hooks", "session-start.sh"))
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("script not executable: mode=%v", info.Mode())
	}
}

func TestSessionStartShellBehavior(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	root := repoRoot(t)
	cmd := exec.Command("bash", filepath.Join(root, "hooks", "tests", "run.sh"))
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hooks/tests/run.sh failed: %v\n%s", err, out)
	}
}

func TestSessionStartShellcheck(t *testing.T) {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("shellcheck not installed")
	}
	root := repoRoot(t)
	cmd := exec.Command(
		"shellcheck",
		"--severity=warning",
		filepath.Join(root, "hooks", "session-start.sh"),
		filepath.Join(root, "hooks", "tests", "run.sh"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shellcheck failed: %s", out)
	}
}
