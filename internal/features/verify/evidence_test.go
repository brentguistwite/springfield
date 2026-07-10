package verify_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/features/verify"
)

// readVerifyMeta reads and decodes verify.json from a round directory into a
// generic map so the test asserts on the on-disk wire shape, not Go structs.
func readVerifyMeta(t *testing.T, dir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "verify.json"))
	if err != nil {
		t.Fatalf("read verify.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode verify.json: %v (raw=%s)", err, raw)
	}
	return m
}

// TestWriteEvidence_RecordsNonZeroExitCode is the acceptance boundary test: a
// non-zero exit MUST survive to verify.json as its real value. This pins the
// regression the review evidence writer shipped, where the snapshot was built
// without setting ExitCode and every round was persisted as exit_code 0.
func TestWriteEvidence_RecordsNonZeroExitCode(t *testing.T) {
	root := t.TempDir()
	req := verify.Request{Command: "go test ./...", Dir: "/work/tree"}
	res := verify.Result{ExitCode: 2, Stdout: "out", Stderr: "boom", Duration: 1500 * time.Millisecond}

	dir, err := verify.WriteEvidence(root, 1, req, res)
	if err != nil {
		t.Fatalf("WriteEvidence: %v", err)
	}

	meta := readVerifyMeta(t, dir)
	// JSON numbers decode to float64 through any; compare as such.
	if got := meta["exit_code"]; got != float64(2) {
		t.Fatalf("exit_code = %v, want 2 (a non-zero exit must not be lost as 0)", got)
	}
}

func TestWriteEvidence_RoundDirAndFields(t *testing.T) {
	root := t.TempDir()
	req := verify.Request{Command: "go test ./...", Dir: "/work/tree"}
	res := verify.Result{
		ExitCode: 0,
		Stdout:   "all pass",
		Stderr:   "",
		TimedOut: false,
		Duration: 2 * time.Second,
	}

	dir, err := verify.WriteEvidence(root, 3, req, res)
	if err != nil {
		t.Fatalf("WriteEvidence: %v", err)
	}

	if got := filepath.Base(dir); got != "verify-iter-3" {
		t.Fatalf("round dir = %q, want verify-iter-3", got)
	}
	if filepath.Dir(dir) != root {
		t.Fatalf("round dir parent = %q, want %q", filepath.Dir(dir), root)
	}

	meta := readVerifyMeta(t, dir)
	if meta["command"] != "go test ./..." {
		t.Fatalf("command = %v, want %q", meta["command"], "go test ./...")
	}
	if meta["cwd"] != "/work/tree" {
		t.Fatalf("cwd = %v, want %q", meta["cwd"], "/work/tree")
	}
	if meta["timed_out"] != false {
		t.Fatalf("timed_out = %v, want false", meta["timed_out"])
	}
	if got := meta["duration_ms"]; got != float64(2000) {
		t.Fatalf("duration_ms = %v, want 2000", got)
	}

	stdout, err := os.ReadFile(filepath.Join(dir, "stdout.txt"))
	if err != nil {
		t.Fatalf("read stdout.txt: %v", err)
	}
	if string(stdout) != "all pass" {
		t.Fatalf("stdout.txt = %q, want %q", stdout, "all pass")
	}
	if _, err := os.Stat(filepath.Join(dir, "stderr.txt")); err != nil {
		t.Fatalf("stderr.txt not written: %v", err)
	}
}

func TestWriteEvidence_RecordsTimedOut(t *testing.T) {
	root := t.TempDir()
	req := verify.Request{Command: "sleep 999", Dir: "/work/tree"}
	// A timed-out round: killed by signal so ExitCode is -1 and TimedOut is true.
	res := verify.Result{ExitCode: -1, TimedOut: true, Duration: 20 * time.Minute}

	dir, err := verify.WriteEvidence(root, 1, req, res)
	if err != nil {
		t.Fatalf("WriteEvidence: %v", err)
	}

	meta := readVerifyMeta(t, dir)
	if meta["timed_out"] != true {
		t.Fatalf("timed_out = %v, want true", meta["timed_out"])
	}
	if got := meta["exit_code"]; got != float64(-1) {
		t.Fatalf("exit_code = %v, want -1 on a timeout kill", got)
	}
}

// TestWriteEvidence_TailTruncatesStreams proves oversized output is truncated
// from the FRONT (the tail is kept) so the failing test's final lines — where
// the actionable error lives — survive, and a notice marks the elision.
func TestWriteEvidence_TailTruncatesStreams(t *testing.T) {
	root := t.TempDir()
	// A payload comfortably larger than any reasonable tail limit, framed with
	// unique markers at both ends so we can tell which end was kept.
	big := "HEAD_MARKER\n" + strings.Repeat("x\n", 400*1024) + "TAIL_MARKER\n"
	req := verify.Request{Command: "noisy", Dir: "/work/tree"}
	res := verify.Result{ExitCode: 1, Stdout: big}

	dir, err := verify.WriteEvidence(root, 1, req, res)
	if err != nil {
		t.Fatalf("WriteEvidence: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(dir, "stdout.txt"))
	if err != nil {
		t.Fatalf("read stdout.txt: %v", err)
	}
	s := string(out)
	if len(s) >= len(big) {
		t.Fatalf("stdout.txt not truncated: len=%d, input len=%d", len(s), len(big))
	}
	if !strings.Contains(s, "TAIL_MARKER") {
		t.Fatalf("stdout.txt dropped the tail — the actionable error would be lost")
	}
	if strings.Contains(s, "HEAD_MARKER") {
		t.Fatalf("stdout.txt kept the head; truncation should drop the front, keep the tail")
	}
	if !strings.Contains(s, "truncated") {
		t.Fatalf("stdout.txt lacks a truncation notice: %q...", s[:64])
	}
}
