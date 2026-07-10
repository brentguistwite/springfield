package verify

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// streamTailLimitBytes caps each captured stream persisted to disk. Verify
// output (a full `go test ./...` transcript) can be large; the actionable
// failure lives at the END, so oversized streams are truncated from the front
// and only the tail is kept. Sized to match the review evidence writer's
// per-file ceiling.
const streamTailLimitBytes = 256 * 1024

// verifyMeta is the on-disk shape of verify.json. exit_code is a required,
// non-omitempty field precisely so a non-zero (or -1 timeout-kill) exit is
// always recorded — the failure mode the review evidence writer shipped, where
// the code was never assigned and every round persisted as 0.
type verifyMeta struct {
	Command    string `json:"command"`
	Cwd        string `json:"cwd"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
}

// WriteEvidence writes purpose-built verify evidence for one round into
// <evidenceDir>/verify-iter-<round>/: verify.json (command, cwd, exit_code,
// duration, timed_out) plus tail-truncated stdout.txt and stderr.txt. It
// returns the round directory it wrote.
//
// The whole verify.Result is passed in (not re-copied field by field at the
// call site) so the exit code cannot be silently dropped: constructing the
// evidence requires the real Result, which already carries the true code.
func WriteEvidence(evidenceDir string, round int, req Request, res Result) (string, error) {
	dir := filepath.Join(evidenceDir, fmt.Sprintf("verify-iter-%d", round))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return dir, err
	}

	meta := verifyMeta{
		Command:    req.Command,
		Cwd:        req.Dir,
		ExitCode:   res.ExitCode,
		DurationMS: res.Duration.Milliseconds(),
		TimedOut:   res.TimedOut,
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return dir, err
	}
	if err := os.WriteFile(filepath.Join(dir, "verify.json"), metaBytes, 0o644); err != nil {
		return dir, err
	}

	if err := os.WriteFile(filepath.Join(dir, "stdout.txt"), tailTruncate(res.Stdout), 0o644); err != nil {
		return dir, err
	}
	if err := os.WriteFile(filepath.Join(dir, "stderr.txt"), tailTruncate(res.Stderr), 0o644); err != nil {
		return dir, err
	}
	return dir, nil
}

// tailTruncate keeps the last streamTailLimitBytes of s, prefixing a notice
// when the front was dropped so a reader knows the transcript is elided.
func tailTruncate(s string) []byte {
	if len(s) <= streamTailLimitBytes {
		return []byte(s)
	}
	tail := s[len(s)-streamTailLimitBytes:]
	notice := fmt.Sprintf("[springfield: output truncated, showing last %d of %d bytes]\n", streamTailLimitBytes, len(s))
	return []byte(notice + tail)
}
