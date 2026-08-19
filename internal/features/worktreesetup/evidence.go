package worktreesetup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// completionMarkerName is the sentinel file written under <evidenceDir>/setup/
// once a setup run has EXITED ZERO. Its presence — not the mere existence of the
// slice worktree — is the durable proof that setup finished, so a resume that
// reuses a worktree whose setup crashed midway (worktree created, deps only
// half-installed) re-runs setup instead of silently dispatching an agent into a
// broken tree. A leading dot keeps it out of casual evidence listings.
//
// The marker's CONTENT is the digest of the command AND the injected setup env
// that earned it, so a resume whose [setup] command OR per-slice env changed
// between runs does not trust a marker written under the old inputs — see
// IsComplete. The env matters because setup runs with injected values like
// SPRINGFIELD_PORT: if the operator moves the [ports] base in springfield.toml,
// a resumed slice's command is unchanged but the port a setup script baked into
// a generated file is now stale, so a command-only key would wrongly skip setup.
// This is the surgical alternative to folding these inputs into the
// worktree-reuse InputDigest: a changed command/env must re-run setup, but must
// NOT nuke and recreate the reused worktree (which would discard the agent's
// committed progress).
const completionMarkerName = ".completed"

// setupDigest is the stable key stored in (and compared against) the completion
// marker. The command is trimmed so whitespace-only edits don't force a re-run,
// and prefixed "sha256:" so a future digest-format change is detectable. The
// injected env is folded in after the command: entries are sorted by key and
// appended as "\x00k=v" lines (a leading NUL per entry — a byte that cannot
// appear in a shell command string or env key/value, so no command text or env
// pair can collide with the separator or forge a boundary). A nil and an empty
// map append nothing, so they digest identically.
func setupDigest(command string, env map[string]string) string {
	h := sha256.New()
	h.Write([]byte(strings.TrimSpace(command)))
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte("\x00" + k + "=" + env[k]))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// MarkComplete records that setup finished successfully for the worktree whose
// evidence lives under evidenceDir, stamping the marker with the digest of the
// command and injected env that succeeded. Written only after an exit-zero run;
// the caller pairs it with ClearComplete so a marker can never outlive the run
// that earned it.
func MarkComplete(evidenceDir, command string, env map[string]string) error {
	dir := filepath.Join(evidenceDir, "setup")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, completionMarkerName), []byte(setupDigest(command, env)), 0o644)
}

// IsComplete reports whether a successful setup for THIS command and env was
// recorded for evidenceDir. A missing marker (including a never-run setup)
// reports false, and so does a marker whose stored digest does not match the
// command+env — a changed [setup] block OR a changed injected env (e.g. a moved
// [ports] base) on a reuse resume must re-run rather than trust the prior run's
// success.
func IsComplete(evidenceDir, command string, env map[string]string) bool {
	data, err := os.ReadFile(filepath.Join(evidenceDir, "setup", completionMarkerName))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == setupDigest(command, env)
}

// ClearComplete removes any prior completion marker for evidenceDir. It is
// called immediately BEFORE a setup run so a crash mid-run leaves no stale
// "completed" record for the next reuse to trust. A missing marker is not an
// error.
func ClearComplete(evidenceDir string) error {
	err := os.Remove(filepath.Join(evidenceDir, "setup", completionMarkerName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
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
