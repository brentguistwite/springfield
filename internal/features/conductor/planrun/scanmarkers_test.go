package planrun_test

import (
	"testing"
	"time"

	coreexec "springfield/internal/core/exec"
	"springfield/internal/features/conductor/planrun"
)

func stdoutEvent(data string) coreexec.Event {
	return coreexec.Event{Type: coreexec.EventStdout, Data: data, Time: time.Now()}
}

func stderrEvent(data string) coreexec.Event {
	return coreexec.Event{Type: coreexec.EventStderr, Data: data, Time: time.Now()}
}

func TestScanMarkersCompleteSingle(t *testing.T) {
	events := []coreexec.Event{
		stdoutEvent("some text"),
		stdoutEvent("<promise>COMPLETE</promise>"),
	}
	passed, complete := planrun.ScanMarkers(events)
	if !complete {
		t.Fatal("expected complete=true")
	}
	if len(passed) != 0 {
		t.Fatalf("expected no passed IDs, got %v", passed)
	}
}

func TestScanMarkersMultipleStoryPassPreservesOrder(t *testing.T) {
	events := []coreexec.Event{
		stdoutEvent("<story-pass>US-003</story-pass>"),
		stdoutEvent("<story-pass>US-001</story-pass>"),
		stdoutEvent("<story-pass>US-002</story-pass>"),
	}
	passed, complete := planrun.ScanMarkers(events)
	if complete {
		t.Fatal("expected complete=false")
	}
	if len(passed) != 3 {
		t.Fatalf("expected 3 passed IDs, got %v", passed)
	}
	if passed[0] != "US-003" || passed[1] != "US-001" || passed[2] != "US-002" {
		t.Fatalf("order not preserved: %v", passed)
	}
}

func TestScanMarkersDeduplicatesStoryPass(t *testing.T) {
	events := []coreexec.Event{
		stdoutEvent("<story-pass>US-001</story-pass>"),
		stdoutEvent("<story-pass>US-001</story-pass>"),
		stdoutEvent("<story-pass>US-002</story-pass>"),
	}
	passed, _ := planrun.ScanMarkers(events)
	if len(passed) != 2 {
		t.Fatalf("expected deduplication to 2 IDs, got %v", passed)
	}
	if passed[0] != "US-001" || passed[1] != "US-002" {
		t.Fatalf("unexpected order after dedup: %v", passed)
	}
}

func TestScanMarkersCasingVariantsNotMatched(t *testing.T) {
	events := []coreexec.Event{
		stdoutEvent("<Promise>complete</Promise>"),
		stdoutEvent("<STORY-PASS>US-001</STORY-PASS>"),
	}
	passed, complete := planrun.ScanMarkers(events)
	if complete {
		t.Fatal("casing variants must not match COMPLETE")
	}
	if len(passed) != 0 {
		t.Fatalf("casing variants must not match story-pass, got %v", passed)
	}
}

func TestScanMarkersWhitespaceVariantsNotMatched(t *testing.T) {
	events := []coreexec.Event{
		stdoutEvent("<promise> COMPLETE </promise>"),
		stdoutEvent("< story-pass >US-001</ story-pass >"),
	}
	passed, complete := planrun.ScanMarkers(events)
	if complete {
		t.Fatal("whitespace variants must not match COMPLETE")
	}
	if len(passed) != 0 {
		t.Fatalf("whitespace variants must not match story-pass, got %v", passed)
	}
}

func TestScanMarkersStderrNotMatched(t *testing.T) {
	events := []coreexec.Event{
		stderrEvent("<promise>COMPLETE</promise>"),
		stderrEvent("<story-pass>US-001</story-pass>"),
	}
	passed, complete := planrun.ScanMarkers(events)
	if complete {
		t.Fatal("stderr events must not be scanned for COMPLETE")
	}
	if len(passed) != 0 {
		t.Fatalf("stderr events must not be scanned for story-pass, got %v", passed)
	}
}

// TestScanMarkersMarkerSplitAcrossTwoEventsNotMatched is a negative regression test.
// Markers split across two stdout events must NOT match; only whole-event scanning.
func TestScanMarkersMarkerSplitAcrossTwoEventsNotMatched(t *testing.T) {
	events := []coreexec.Event{
		stdoutEvent("<promise>"),
		stdoutEvent("COMPLETE</promise>"),
	}
	_, complete := planrun.ScanMarkers(events)
	if complete {
		t.Fatal("marker split across two events must NOT be detected (regression test)")
	}
}

func TestScanMarkersEmptyEventsNoMarkers(t *testing.T) {
	passed, complete := planrun.ScanMarkers(nil)
	if complete {
		t.Fatal("nil events should not be complete")
	}
	if len(passed) != 0 {
		t.Fatalf("nil events should have no passed IDs")
	}
}

// TestScanMarkersMarkerInJSONFalsePositive tests the accepted v1 trade-off:
// a story-pass marker literally inside a JSON field IS detected (text-search).
// This is intentional — marker tags are unusual enough that incidental collisions
// are vanishingly unlikely. Promote to JSON-aware extraction if a real adapter trips it.
func TestScanMarkersMarkerInJSONFalsePositive(t *testing.T) {
	events := []coreexec.Event{
		stdoutEvent(`{"error": "unexpected <story-pass>US-007</story-pass> token"}`),
	}
	passed, _ := planrun.ScanMarkers(events)
	if len(passed) != 1 || passed[0] != "US-007" {
		t.Fatalf("v1 trade-off: marker in JSON field should be detected, got %v", passed)
	}
}

func TestScanMarkersFullIDUSPrefix(t *testing.T) {
	events := []coreexec.Event{
		stdoutEvent("<story-pass>US-042</story-pass>"),
	}
	passed, _ := planrun.ScanMarkers(events)
	if len(passed) != 1 || passed[0] != "US-042" {
		t.Fatalf("expected US-042 with US- prefix, got %v", passed)
	}
}
