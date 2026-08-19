package worktreesetup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"
)

// streamTailLimitBytes caps each captured stream persisted to disk. Setup
// output (a full `npm install` transcript) can be large; the actionable failure
// lives at the END, so oversized streams are truncated from the front and only
// the tail is kept. Sized to match the verify evidence writer's per-file
// ceiling.
const streamTailLimitBytes = 256 * 1024

// setupMeta is the on-disk shape of setup.json. exit_code is a required,
// non-omitempty field so a non-zero (or -1 timeout-kill) exit is always
// recorded.
type setupMeta struct {
	Command    string `json:"command"`
	Cwd        string `json:"cwd"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
}

// WriteEvidence writes setup evidence into <evidenceDir>/setup/: setup.json
// (command, cwd, exit_code, duration, timed_out) plus tail-truncated stdout.txt
// and stderr.txt. It returns the directory it wrote.
//
// The whole Result is passed in (not re-copied field by field at the call site)
// so the exit code cannot be silently dropped: constructing the evidence
// requires the real Result, which already carries the true code.
func WriteEvidence(evidenceDir string, req Request, res Result) (string, error) {
	dir := filepath.Join(evidenceDir, "setup")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return dir, err
	}

	meta := setupMeta{
		Command:    req.Command,
		Cwd:        req.WorktreeRoot,
		ExitCode:   res.ExitCode,
		DurationMS: res.Duration.Milliseconds(),
		TimedOut:   res.TimedOut,
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return dir, err
	}
	if err := os.WriteFile(filepath.Join(dir, "setup.json"), metaBytes, 0o644); err != nil {
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
	// The raw byte cut can land inside a multi-byte rune, leaving invalid UTF-8
	// as leading continuation bytes. Advance to the next rune boundary so the
	// persisted evidence is always valid UTF-8.
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	notice := fmt.Sprintf("[springfield: output truncated, showing last %d of %d bytes]\n", streamTailLimitBytes, len(s))
	return []byte(notice + tail)
}
