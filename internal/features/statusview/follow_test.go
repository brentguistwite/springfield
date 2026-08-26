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
	_ = f.Close()

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

// TestTraceFollowerResetsOffsetOnRollover pins the follow-across-restart
// contract: a batch restart writes a NEW trace file (fresh timestamp), so the
// follower must reset its offset to 0 when the path changes — otherwise it
// Seeks the stale, large offset into the new, smaller file and skips the head
// of the new stream. Tailing the same path resumes; a changed path restarts.
func TestTraceFollowerResetsOffsetOnRollover(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "batch-20260820T120000Z.agent-trace.jsonl")
	pathB := filepath.Join(dir, "batch-20260820T130000Z.agent-trace.jsonl")

	// File A: several complete event lines for plan 01, fully consumed so the
	// follower's offset advances well past the length of the (shorter) file B.
	fileA := `{"type":"stdout","time":"t","data":"alpha one","plan":"01"}
{"type":"stdout","time":"t","data":"alpha two","plan":"01"}
{"type":"stdout","time":"t","data":"alpha three","plan":"01"}
`
	if err := os.WriteFile(pathA, []byte(fileA), 0o644); err != nil {
		t.Fatalf("write A: %v", err)
	}

	var fol statusview.TraceFollower
	var buf bytes.Buffer
	if err := fol.Tail(&buf, pathA, "01"); err != nil {
		t.Fatalf("Tail A: %v", err)
	}
	if !strings.Contains(buf.String(), "alpha one") || !strings.Contains(buf.String(), "alpha three") {
		t.Fatalf("file A lines missing:\n%s", buf.String())
	}

	// File B: the restart's trace, shorter than the consumed offset into A. A
	// bare offset carried across the switch would Seek past B's head.
	fileB := `{"type":"stdout","time":"t","data":"bravo one","plan":"01"}
{"type":"stdout","time":"t","data":"bravo two","plan":"01"}
`
	if err := os.WriteFile(pathB, []byte(fileB), 0o644); err != nil {
		t.Fatalf("write B: %v", err)
	}

	buf.Reset()
	if err := fol.Tail(&buf, pathB, "01"); err != nil {
		t.Fatalf("Tail B: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "bravo one") || !strings.Contains(out, "bravo two") {
		t.Fatalf("rollover skipped the head of the new trace:\n%s", out)
	}
	if strings.Contains(out, "alpha") {
		t.Fatalf("re-emitted the old trace on rollover:\n%s", out)
	}
}

// TestTraceFollowerDrainsOldTailOnRollover pins the no-drop-on-roll contract:
// when the batch rolls over to a new trace file while the OLD file still holds
// event lines the follower has not yet streamed (events appended after the last
// tick but before the roll — reachable because a follow loop stays alive across
// an interrupt, so a resume can roll the trace mid-follow), those final old-file
// lines are drained before the switch, not lost. The prior rollover test fully
// consumed file A first, so it never exercised an unread tail at roll time.
func TestTraceFollowerDrainsOldTailOnRollover(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "batch-20260820T120000Z.agent-trace.jsonl")
	pathB := filepath.Join(dir, "batch-20260820T130000Z.agent-trace.jsonl")

	// File A, first tick: only the first line exists when the follower reads it.
	if err := os.WriteFile(pathA, []byte(`{"type":"stdout","time":"t","data":"alpha one","plan":"01"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write A: %v", err)
	}
	var fol statusview.TraceFollower
	var buf bytes.Buffer
	if err := fol.Tail(&buf, pathA, "01"); err != nil {
		t.Fatalf("Tail A: %v", err)
	}
	if !strings.Contains(buf.String(), "alpha one") {
		t.Fatalf("file A first line missing:\n%s", buf.String())
	}

	// More events land in A AFTER that tick (still the same file), then the batch
	// resumes onto a fresh trace file B — all before the follower's next tick.
	fa, err := os.OpenFile(pathA, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open A append: %v", err)
	}
	if _, err := fa.WriteString(`{"type":"stdout","time":"t","data":"alpha two","plan":"01"}` + "\n"); err != nil {
		t.Fatalf("append A: %v", err)
	}
	_ = fa.Close()
	if err := os.WriteFile(pathB, []byte(`{"type":"stdout","time":"t","data":"bravo one","plan":"01"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write B: %v", err)
	}

	// Next tick observes the rolled path: the unread A tail must still surface,
	// and B's head must surface too — nothing dropped, nothing re-emitted.
	buf.Reset()
	if err := fol.Tail(&buf, pathB, "01"); err != nil {
		t.Fatalf("Tail B: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "alpha two") {
		t.Fatalf("rollover dropped the old file's unread tail:\n%s", out)
	}
	if !strings.Contains(out, "bravo one") {
		t.Fatalf("rollover skipped the head of the new trace:\n%s", out)
	}
	if strings.Contains(out, "alpha one") {
		t.Fatalf("re-emitted an already-streamed old-file line:\n%s", out)
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
