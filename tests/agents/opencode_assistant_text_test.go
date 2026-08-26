package agents_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/agents/opencode"
	coreexec "springfield/internal/core/exec"
)

// AssistantText decodes part.text from type:"text" events whose time.end is
// set (finalized text), newline-joined, so the review gate scans real
// newlines rather than raw escaped NDJSON (BUG-1 class).
func TestOpencodeAssistantTextJoinsTextParts(t *testing.T) {
	events := loadFixtureEvents(t, filepath.Join("fixtures", "opencode", "success.jsonl"))

	text := mustDecoder(t, opencode.New(exec.LookPath)).AssistantText(events)
	if text == "" {
		t.Fatal("expected non-empty assistant text")
	}
	if !strings.Contains(text, "sc.txt") {
		t.Fatalf("decoded text missing capture's assistant prose: %q", text)
	}
	if strings.HasPrefix(strings.TrimSpace(text), "{") {
		t.Fatalf("decoded text must be plain text, not raw JSON: %q", text)
	}
}

func TestOpencodeAssistantTextMultiPartNewlineJoined(t *testing.T) {
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"type":"text","part":{"type":"text","text":"first line","time":{"start":1,"end":2}}}`},
		{Type: coreexec.EventStdout, Data: `{"type":"text","part":{"type":"text","text":"second line","time":{"start":3,"end":4}}}`},
	}

	got := mustDecoder(t, opencode.New(exec.LookPath)).AssistantText(events)
	want := "first line\nsecond line"
	if got != want {
		t.Fatalf("AssistantText() = %q, want %q", got, want)
	}
}

// Un-finalized text (time.end unset/zero) is streaming dregs — excluded.
func TestOpencodeAssistantTextExcludesUnfinalizedParts(t *testing.T) {
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"type":"text","part":{"type":"text","text":"finalized","time":{"start":1,"end":2}}}`},
		{Type: coreexec.EventStdout, Data: `{"type":"text","part":{"type":"text","text":"streaming dregs","time":{"start":3,"end":0}}}`},
	}

	got := mustDecoder(t, opencode.New(exec.LookPath)).AssistantText(events)
	if got != "finalized" {
		t.Fatalf("AssistantText() = %q, want %q", got, "finalized")
	}
}

func TestOpencodeAssistantTextEmptyWhenNoTextEvents(t *testing.T) {
	d := mustDecoder(t, opencode.New(exec.LookPath))
	if got := d.AssistantText(nil); got != "" {
		t.Fatalf("AssistantText(nil) = %q, want empty", got)
	}
	events := loadFixtureEvents(t, filepath.Join("fixtures", "opencode", "hard-error.jsonl"))
	if got := d.AssistantText(events); got != "" {
		t.Fatalf("AssistantText(error events) = %q, want empty", got)
	}
}
