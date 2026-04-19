package cmd_test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// randEvilPath returns a unique absolute path under t.TempDir() for a symlink
// target. Using the per-test tempdir keeps the machine clean and avoids
// cross-test clobber.
func randEvilPath(t *testing.T) string {
	t.Helper()
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return filepath.Join(t.TempDir(), "evil-"+hex.EncodeToString(buf[:]))
}

// TestRestoreReplacesSymlinkInsteadOfFollowing verifies that on tamper
// detection, the restore step replaces a symlink planted at
// planDir/batch.json with a regular file containing snapshot bytes — it
// must NOT follow the link into the attacker-chosen target.
func TestRestoreReplacesSymlinkInsteadOfFollowing(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}

	evil := randEvilPath(t)
	if err := os.WriteFile(evil, []byte("EVIL-ORIGINAL"), 0o644); err != nil {
		t.Fatalf("seed evil: %v", err)
	}

	// Fake agent swaps batch.json for a symlink pointing at evil.
	fakeBinDir := filepath.Join(dir, "bin")
	tamperCmd := fmt.Sprintf(
		"for f in %s/*/batch.json; do rm -f \"$f\"; ln -s %q \"$f\"; done",
		filepath.Join(dir, ".springfield", "plans"), evil)
	installTamperingAgent(t, fakeBinDir, "claude", tamperCmd)

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected tamper failure, got:\n%s", output)
	}
	if !strings.Contains(output, "state tampered") && !strings.Contains(output, "plan dir unreadable") {
		t.Errorf("expected tamper or unreadable message, got:\n%s", output)
	}

	// Evil target MUST be untouched: still the seeded bytes. This is the
	// load-bearing assertion for F1 — if restore followed the symlink, the
	// snapshot bytes would have been written into the attacker's chosen
	// path.
	evilBytes, err := os.ReadFile(evil)
	if err != nil {
		t.Fatalf("read evil target: %v", err)
	}
	if string(evilBytes) != "EVIL-ORIGINAL" {
		t.Errorf("evil target was modified — restore followed symlink; got: %q", string(evilBytes))
	}

	// Evil target must still be a regular file (not a symlink or device).
	li, err := os.Lstat(evil)
	if err != nil {
		t.Fatalf("lstat evil: %v", err)
	}
	if !li.Mode().IsRegular() {
		t.Errorf("evil target mode changed unexpectedly: %v", li.Mode())
	}
}

// TestRestoreReplacesSymlinkForRunJson verifies run.json is restored
// correctly when an agent swaps it for a symlink.
func TestRestoreReplacesSymlinkForRunJson(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}

	evil := randEvilPath(t)
	if err := os.WriteFile(evil, []byte("EVIL-RUN-ORIGINAL"), 0o644); err != nil {
		t.Fatalf("seed evil: %v", err)
	}

	runJSON := filepath.Join(dir, ".springfield", "run.json")
	fakeBinDir := filepath.Join(dir, "bin")
	tamperCmd := fmt.Sprintf("rm -f %q; ln -s %q %q", runJSON, evil, runJSON)
	installTamperingAgent(t, fakeBinDir, "claude", tamperCmd)

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected tamper failure, got:\n%s", output)
	}

	evilBytes, err := os.ReadFile(evil)
	if err != nil {
		t.Fatalf("read evil target: %v", err)
	}
	if string(evilBytes) != "EVIL-RUN-ORIGINAL" {
		t.Errorf("evil target was modified during run.json restore; got: %q", string(evilBytes))
	}

	// After restore, run.json is cleared by ClearRun — it's legitimate for
	// the file to not exist at all. But if it does exist, it must be a
	// regular file (not a lingering symlink).
	if li, err := os.Lstat(runJSON); err == nil {
		if !li.Mode().IsRegular() {
			t.Errorf("run.json still a non-regular node after restore+clear; mode=%v", li.Mode())
		}
	}
}

// TestRestoreRegularFileUnchanged is a regression guard: the symlink-safe
// restore path must still work for the common case of a regular file that
// the agent merely rewrote.
func TestRestoreRegularFileUnchanged(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}

	fakeBinDir := filepath.Join(dir, "bin")
	installTamperingAgent(t, fakeBinDir, "claude",
		"for f in "+filepath.Join(dir, ".springfield", "plans")+"/*/batch.json; do echo 'rewritten' > \"$f\"; done")

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected tamper failure, got:\n%s", output)
	}
	if !strings.Contains(output, "state tampered") {
		t.Errorf("expected tamper message, got:\n%s", output)
	}

	// Restore error must not surface in the output. If writeFileReplacing-
	// NonRegular broke the regular-file happy path, the tamper message would
	// include "; restore failed:".
	if strings.Contains(output, "restore failed") {
		t.Errorf("restore should not have failed on regular-file tamper, got:\n%s", output)
	}
}
