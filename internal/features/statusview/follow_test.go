package statusview_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/statusview"
)

// twoPlanTrace is an agent-trace JSONL fragment interleaving two plans' events,
// exactly as the runner's serialized append writes them (one shared file, each
// line stamped with its plan id).
const twoPlanTrace = `{"type":"stdout","time":"2026-08-20T12:00:00Z","data":"alpha thinking","plan":"01"}
{"type":"stdout","time":"2026-08-20T12:00:01Z","data":"bravo thinking","plan":"02"}
{"type":"stderr","time":"2026-08-20T12:00:02Z","data":"alpha warned","plan":"01"}
{"type":"stdout","time":"2026-08-20T12:00:03Z","data":"bravo done","plan":"02"}
`

// TestFilterTraceRendersOnlySelectedPlan pins the core follow contract: fed a
// trace containing two plans' interleaved events, exactly one plan's data lines
// render — the other plan never leaks in.
func TestFilterTraceRendersOnlySelectedPlan(t *testing.T) {
	var buf bytes.Buffer
	if err := statusview.FilterTrace(&buf, strings.NewReader(twoPlanTrace), "01"); err != nil {
		t.Fatalf("FilterTrace: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "alpha thinking") || !strings.Contains(out, "alpha warned") {
		t.Fatalf("selected plan 01 lines missing:\n%s", out)
	}
	if strings.Contains(out, "bravo") {
		t.Fatalf("plan 02 leaked into plan 01 follow:\n%s", out)
	}
	// One line per matching event, no more.
	if got := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; got != 2 {
		t.Fatalf("want 2 rendered lines for plan 01, got %d:\n%s", got, out)
	}
}

// TestFilterTraceSkipsMalformedAndEmpty pins the best-effort contract: a
// malformed JSONL line and an event with empty data are skipped, not fatal.
func TestFilterTraceSkipsMalformedAndEmpty(t *testing.T) {
	trace := `{not json
{"type":"lifecycle","time":"t","data":"","plan":"01"}
{"type":"stdout","time":"t","data":"real line","plan":"01"}
`
	var buf bytes.Buffer
	if err := statusview.FilterTrace(&buf, strings.NewReader(trace), "01"); err != nil {
		t.Fatalf("FilterTrace: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "real line" {
		t.Fatalf("want only the real line, got:\n%q", got)
	}
}

// TestLatestTracePathPicksNewest pins that follow resolves the newest trace
// file for a batch (timestamp segment sorts lexically = chronologically) and
// reports ok=false when the batch has written no trace yet.
func TestLatestTracePathPicksNewest(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, ".springfield", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, ok, err := statusview.LatestTracePath(root, "batch-001"); err != nil || ok {
		t.Fatalf("want ok=false before any trace, got ok=%v err=%v", ok, err)
	}

	for _, name := range []string{
		"batch-001-20260820T120000Z.agent-trace.jsonl",
		"batch-001-20260820T130000Z.agent-trace.jsonl",
		"batch-002-20260820T140000Z.agent-trace.jsonl", // different batch, ignored
	} {
		if err := os.WriteFile(filepath.Join(logs, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	path, ok, err := statusview.LatestTracePath(root, "batch-001")
	if err != nil || !ok {
		t.Fatalf("want ok=true, got ok=%v err=%v", ok, err)
	}
	if filepath.Base(path) != "batch-001-20260820T130000Z.agent-trace.jsonl" {
		t.Fatalf("want newest batch-001 trace, got %s", filepath.Base(path))
	}
}

// TestTailTraceAdvancesOnlyCompleteLines pins the read-only offset-tail: only
// whole lines are consumed, and a subsequent tick resumes from the returned
// offset (no re-emit of already-streamed lines, no drop of a partial trailing
// line).
func TestTailTraceAdvancesOnlyCompleteLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")

	first := `{"type":"stdout","time":"t","data":"line one","plan":"01"}
{"type":"stdout","time":"t","data":"partial`
	if err := os.WriteFile(path, []byte(first), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var buf bytes.Buffer
	off, err := statusview.TailTrace(&buf, path, 0, "01")
	if err != nil {
		t.Fatalf("TailTrace: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "line one" {
		t.Fatalf("want only the complete line, got:\n%q", got)
	}

	// The partial line completes and a new event arrives.
	rest := `two","plan":"01"}
{"type":"stdout","time":"t","data":"line three","plan":"01"}
`
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	if _, err := f.WriteString(rest); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	buf.Reset()
	if _, err := statusview.TailTrace(&buf, path, off, "01"); err != nil {
		t.Fatalf("TailTrace 2: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "line one") {
		t.Fatalf("re-emitted an already-streamed line:\n%s", out)
	}
	if !strings.Contains(out, "partialtwo") || !strings.Contains(out, "line three") {
		t.Fatalf("did not resume the once-partial and new lines:\n%s", out)
	}
}

// TestTailTraceMissingFileIsNotAnError pins that a not-yet-created trace (the
// runner writes it lazily on the first agent event) returns the offset
// unchanged with no error, so the follow loop keeps polling.
func TestTailTraceMissingFileIsNotAnError(t *testing.T) {
	var buf bytes.Buffer
	off, err := statusview.TailTrace(&buf, filepath.Join(t.TempDir(), "nope.jsonl"), 0, "01")
	if err != nil {
		t.Fatalf("missing trace should not error: %v", err)
	}
	if off != 0 || buf.Len() != 0 {
		t.Fatalf("want offset 0 and no output, got off=%d out=%q", off, buf.String())
	}
}
