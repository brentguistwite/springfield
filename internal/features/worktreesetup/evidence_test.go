package worktreesetup_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"springfield/internal/features/worktreesetup"
)

// TestCompletionMarkerRoundTrip proves the marker helpers form a coherent
// record: absent by default, present after MarkComplete, and cleared by
// ClearComplete (which is a no-op when the marker was never written). This is
// the durable "setup already succeeded" signal the runner gates a reuse resume
// on, distinct from the mere existence of the worktree.
func TestCompletionMarkerRoundTrip(t *testing.T) {
	evidenceDir := t.TempDir()

	if worktreesetup.IsComplete(evidenceDir) {
		t.Fatalf("marker reported complete before any MarkComplete")
	}
	// ClearComplete on a never-written marker must not error.
	if err := worktreesetup.ClearComplete(evidenceDir); err != nil {
		t.Fatalf("ClearComplete on missing marker: %v", err)
	}

	if err := worktreesetup.MarkComplete(evidenceDir); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	if !worktreesetup.IsComplete(evidenceDir) {
		t.Fatalf("marker not reported complete after MarkComplete")
	}

	if err := worktreesetup.ClearComplete(evidenceDir); err != nil {
		t.Fatalf("ClearComplete: %v", err)
	}
	if worktreesetup.IsComplete(evidenceDir) {
		t.Fatalf("marker still reported complete after ClearComplete")
	}
}

// TestWriteEvidence_PersistsMetaAndStreams proves setup output is captured under
// <evidenceDir>/setup/: setup.json plus stdout.txt and stderr.txt.
func TestWriteEvidence_PersistsMetaAndStreams(t *testing.T) {
	evidenceDir := t.TempDir()
	req := worktreesetup.Request{Command: "npm install", WorktreeRoot: "/wt"}
	res := worktreesetup.Result{
		ExitCode: 1,
		Stdout:   "installing...\n",
		Stderr:   "boom\n",
		Duration: 250 * time.Millisecond,
		TimedOut: false,
	}
	dir, err := worktreesetup.WriteEvidence(evidenceDir, req, res)
	if err != nil {
		t.Fatalf("WriteEvidence: %v", err)
	}
	if got := filepath.Base(dir); got != "setup" {
		t.Fatalf("evidence dir base = %q, want setup", got)
	}

	metaBytes, err := os.ReadFile(filepath.Join(dir, "setup.json"))
	if err != nil {
		t.Fatalf("read setup.json: %v", err)
	}
	var meta struct {
		Command    string `json:"command"`
		Cwd        string `json:"cwd"`
		ExitCode   int    `json:"exit_code"`
		DurationMS int64  `json:"duration_ms"`
		TimedOut   bool   `json:"timed_out"`
	}
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("unmarshal setup.json: %v", err)
	}
	if meta.Command != "npm install" || meta.Cwd != "/wt" {
		t.Errorf("meta command/cwd = %q/%q", meta.Command, meta.Cwd)
	}
	// exit_code must be recorded even when non-zero (the whole failure signal).
	if meta.ExitCode != 1 {
		t.Errorf("exit_code = %d, want 1", meta.ExitCode)
	}
	if meta.DurationMS != 250 {
		t.Errorf("duration_ms = %d, want 250", meta.DurationMS)
	}

	stdout, _ := os.ReadFile(filepath.Join(dir, "stdout.txt"))
	if string(stdout) != "installing...\n" {
		t.Errorf("stdout.txt = %q", stdout)
	}
	stderr, _ := os.ReadFile(filepath.Join(dir, "stderr.txt"))
	if string(stderr) != "boom\n" {
		t.Errorf("stderr.txt = %q", stderr)
	}
}
