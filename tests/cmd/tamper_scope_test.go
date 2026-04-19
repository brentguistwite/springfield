package cmd_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTamperDetectsWriteToPlanDirFile verifies the snapshot walks the whole
// plan dir. An agent that creates a new file under .springfield/plans/<id>/
// trips tamper detection with a relpath-naming reason.
func TestTamperDetectsWriteToPlanDirFile(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}

	plansRoot := filepath.Join(dir, ".springfield", "plans")
	fakeBinDir := filepath.Join(dir, "bin")
	installTamperingAgent(t, fakeBinDir, "claude",
		fmt.Sprintf("for d in %s/*; do echo 'spy' > \"$d/notes.md\"; done", plansRoot))

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected tamper on plan-dir write, got:\n%s", output)
	}
	if !strings.Contains(output, "state tampered") {
		t.Errorf("expected 'state tampered' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "notes.md") {
		t.Errorf("expected reason to name notes.md, got:\n%s", output)
	}
}

// TestTamperDetectsDeletedSourceMd verifies deleting a file inside the plan
// dir (beyond batch.json) is caught.
func TestTamperDetectsDeletedSourceMd(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}

	plansRoot := filepath.Join(dir, ".springfield", "plans")
	fakeBinDir := filepath.Join(dir, "bin")
	installTamperingAgent(t, fakeBinDir, "claude",
		fmt.Sprintf("rm -f %s/*/source.md", plansRoot))

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected tamper on source.md delete, got:\n%s", output)
	}
	if !strings.Contains(output, "source.md") {
		t.Errorf("expected reason to name source.md, got:\n%s", output)
	}
}

// TestTamperReasonMentionsBatchJson ensures the existing squibby reproduction
// still surfaces batch.json in the reason string (relpath-based now).
func TestTamperReasonMentionsBatchJson(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}

	plansRoot := filepath.Join(dir, ".springfield", "plans")
	fakeBinDir := filepath.Join(dir, "bin")
	installTamperingAgent(t, fakeBinDir, "claude",
		fmt.Sprintf("for f in %s/*/batch.json; do echo 'not json' > \"$f\"; done", plansRoot))

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected tamper, got:\n%s", output)
	}
	if !strings.Contains(output, "batch.json") {
		t.Errorf("expected 'batch.json' in reason, got:\n%s", output)
	}
}
