package cmd_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findPlanDir returns the first batch plan dir under .springfield/plans or
// fails the test if none exists.
func findPlanDir(t *testing.T, root string) string {
	t.Helper()
	plansRoot := filepath.Join(root, ".springfield", "plans")
	entries, err := os.ReadDir(plansRoot)
	if err != nil {
		t.Fatalf("read plans dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			return filepath.Join(plansRoot, e.Name())
		}
	}
	t.Fatalf("no batch plan dir under %s", plansRoot)
	return ""
}

// TestSnapshotRejectsSymlink — a symlink under the plan dir (stray or
// malicious) must cause the pre-agent snapshot to fail with a non-regular
// entry error, surfaced as a snapshot error on start.
func TestSnapshotRejectsSymlink(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}

	planDir := findPlanDir(t, dir)
	// Plant a symlink inside the plan dir BEFORE start so the pre-agent
	// snapshot sees it.
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("irrelevant"), 0o644); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(planDir, "sneaky.link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Even with a noop agent, snapshot runs BEFORE the agent and must
	// reject the symlink.
	fakeBinDir := filepath.Join(dir, "bin")
	argvPath := filepath.Join(dir, "claude.argv")
	installFakeAgentBinary(t, fakeBinDir, "claude", argvPath)

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected snapshot failure on symlink, got:\n%s", output)
	}
	if !strings.Contains(output, "non-regular") {
		t.Errorf("expected 'non-regular' in output, got:\n%s", output)
	}
}

// TestSnapshotRejectsOversizeFile — a single plan-dir file exceeding the
// per-file cap must cause the snapshot to fail.
func TestSnapshotRejectsOversizeFile(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}

	planDir := findPlanDir(t, dir)
	// 300 KiB > 256 KiB cap.
	big := make([]byte, 300*1024)
	for i := range big {
		big[i] = 'A'
	}
	if err := os.WriteFile(filepath.Join(planDir, "huge.bin"), big, 0o644); err != nil {
		t.Fatalf("write huge: %v", err)
	}

	fakeBinDir := filepath.Join(dir, "bin")
	argvPath := filepath.Join(dir, "claude.argv")
	installFakeAgentBinary(t, fakeBinDir, "claude", argvPath)

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected snapshot failure on oversize file, got:\n%s", output)
	}
	if !strings.Contains(output, "per-file cap") {
		t.Errorf("expected 'per-file cap' in output, got:\n%s", output)
	}
}

// TestSnapshotRejectsOversizeTree — many small files summing above the tree
// cap must cause the snapshot to fail.
func TestSnapshotRejectsOversizeTree(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}

	planDir := findPlanDir(t, dir)
	// Plant 20 files at 200 KiB each = ~4 MiB, well over the 2 MiB total
	// cap but under the 256 KiB per-file cap.
	chunk := make([]byte, 200*1024)
	for i := range chunk {
		chunk[i] = 'B'
	}
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("filler-%02d.bin", i)
		if err := os.WriteFile(filepath.Join(planDir, name), chunk, 0o644); err != nil {
			t.Fatalf("write filler %d: %v", i, err)
		}
	}

	fakeBinDir := filepath.Join(dir, "bin")
	argvPath := filepath.Join(dir, "claude.argv")
	installFakeAgentBinary(t, fakeBinDir, "claude", argvPath)

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected snapshot failure on oversize tree, got:\n%s", output)
	}
	if !strings.Contains(output, "total cap") {
		t.Errorf("expected 'total cap' in output, got:\n%s", output)
	}
}

// TestSnapshotAcceptsNormalTree — the happy path: plan dir contains only
// batch.json + source.md at sane sizes, noop agent passes.
func TestSnapshotAcceptsNormalTree(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}

	fakeBinDir := filepath.Join(dir, "bin")
	argvPath := filepath.Join(dir, "claude.argv")
	installFakeAgentBinary(t, fakeBinDir, "claude", argvPath)

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err != nil {
		t.Fatalf("noop agent should succeed, got err=%v\n%s", err, output)
	}
	if !strings.Contains(output, "Status: completed") {
		t.Errorf("expected completed, got:\n%s", output)
	}
}
