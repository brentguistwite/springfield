package execution

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	coreexec "springfield/internal/core/exec"
)

func TestWriteEvidenceCreatesAllFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evidence")
	startedAt := time.Date(2026, time.April, 30, 12, 0, 0, 0, time.UTC)
	endedAt := startedAt.Add(2 * time.Minute)
	snap := EvidenceSnapshot{
		AgentID:        "claude",
		Model:          "claude-opus-4-7",
		ExitCode:       17,
		Classification: "fatal",
		Prompt:         "ship it",
		Err:            errors.New("boom"),
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: "first line", Time: startedAt},
			{Type: coreexec.EventStderr, Data: "warn", Time: startedAt.Add(time.Second)},
			{Type: coreexec.EventStdout, Data: "second line", Time: endedAt},
		},
		StartedAt: startedAt,
		EndedAt:   endedAt,
	}

	if err := WriteEvidence(dir, snap); err != nil {
		t.Fatalf("WriteEvidence: %v", err)
	}

	for _, name := range []string{"meta.json", "events.jsonl", "assistant_text.txt", "prompt.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("Stat(%s): %v", name, err)
		}
	}

	var meta struct {
		AgentID        string    `json:"agent_id"`
		Model          string    `json:"model"`
		ExitCode       int       `json:"exit_code"`
		Classification string    `json:"classification"`
		StartedAt      time.Time `json:"started_at"`
		EndedAt        time.Time `json:"ended_at"`
		Error          string    `json:"error"`
	}
	metaBytes, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		t.Fatalf("ReadFile(meta.json): %v", err)
	}
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("Unmarshal(meta.json): %v", err)
	}
	if meta.AgentID != snap.AgentID || meta.Model != snap.Model || meta.ExitCode != snap.ExitCode || meta.Classification != snap.Classification {
		t.Fatalf("meta = %#v, want agent/model/exit/classification from snapshot", meta)
	}
	if !meta.StartedAt.Equal(startedAt) || !meta.EndedAt.Equal(endedAt) {
		t.Fatalf("meta times = %v..%v, want %v..%v", meta.StartedAt, meta.EndedAt, startedAt, endedAt)
	}
	if meta.Error != "boom" {
		t.Fatalf("meta error = %q, want %q", meta.Error, "boom")
	}

	promptBytes, err := os.ReadFile(filepath.Join(dir, "prompt.txt"))
	if err != nil {
		t.Fatalf("ReadFile(prompt.txt): %v", err)
	}
	if got := string(promptBytes); got != snap.Prompt {
		t.Fatalf("prompt = %q, want %q", got, snap.Prompt)
	}

	assistantBytes, err := os.ReadFile(filepath.Join(dir, "assistant_text.txt"))
	if err != nil {
		t.Fatalf("ReadFile(assistant_text.txt): %v", err)
	}
	if got, want := string(assistantBytes), "first line\nsecond line"; got != want {
		t.Fatalf("assistant text = %q, want %q", got, want)
	}

	eventsBytes, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(events.jsonl): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(eventsBytes)), "\n")
	if len(lines) != len(snap.Events) {
		t.Fatalf("events lines = %d, want %d", len(lines), len(snap.Events))
	}
}

func TestWriteEvidenceTruncatesAssistantText(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evidence")
	head := "HEAD-MARKER\n" + strings.Repeat("a", 900*1024)
	tail := strings.Repeat("tail", 1024)
	snap := EvidenceSnapshot{
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: head},
			{Type: coreexec.EventStderr, Data: "ignored"},
			{Type: coreexec.EventStdout, Data: tail},
		},
	}

	if err := WriteEvidence(dir, snap); err != nil {
		t.Fatalf("WriteEvidence: %v", err)
	}

	assistantBytes, err := os.ReadFile(filepath.Join(dir, "assistant_text.txt"))
	if err != nil {
		t.Fatalf("ReadFile(assistant_text.txt): %v", err)
	}
	if len(assistantBytes) > assistantTextLimitBytes {
		t.Fatalf("assistant text size = %d, want <= %d", len(assistantBytes), assistantTextLimitBytes)
	}
	if !strings.Contains(string(assistantBytes), tail) {
		t.Fatal("assistant text missing stdout tail")
	}
	if strings.Contains(string(assistantBytes), "HEAD-MARKER") {
		t.Fatal("assistant text retained dropped head marker")
	}
}

func TestWriteEvidenceTruncatesAssistantTextWithoutDroppingInvalidTailBytes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evidence")
	head := strings.Repeat("a", 300*1024)
	invalidTail := string([]byte{0xff, 0xfe, 0xfd})
	tail := "tail-marker"
	snap := EvidenceSnapshot{
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: head},
			{Type: coreexec.EventStdout, Data: invalidTail + tail},
		},
	}

	if err := WriteEvidence(dir, snap); err != nil {
		t.Fatalf("WriteEvidence: %v", err)
	}

	assistantBytes, err := os.ReadFile(filepath.Join(dir, "assistant_text.txt"))
	if err != nil {
		t.Fatalf("ReadFile(assistant_text.txt): %v", err)
	}
	if len(assistantBytes) != assistantTextLimitBytes {
		t.Fatalf("assistant text size = %d, want %d", len(assistantBytes), assistantTextLimitBytes)
	}
	if !strings.Contains(string(assistantBytes), tail) {
		t.Fatalf("assistant text missing retained tail marker: %q", string(assistantBytes[len(assistantBytes)-32:]))
	}
	if !strings.Contains(string(assistantBytes), invalidTail) {
		t.Fatal("assistant text dropped invalid tail bytes")
	}
}

func TestWriteEvidenceEventsJSONLIsLossless(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evidence")
	now := time.Date(2026, time.April, 30, 12, 34, 56, 123456789, time.UTC)
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"k":"v"}`, Time: now},
		{Type: coreexec.EventStderr, Data: "stderr line", Time: now.Add(2 * time.Second)},
	}

	if err := WriteEvidence(dir, EvidenceSnapshot{Events: events}); err != nil {
		t.Fatalf("WriteEvidence: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(events.jsonl): %v", err)
	}
	var roundTrip []coreexec.Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event coreexec.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("Unmarshal(event): %v", err)
		}
		roundTrip = append(roundTrip, event)
	}
	if !reflect.DeepEqual(roundTrip, events) {
		t.Fatalf("round trip events = %#v, want %#v", roundTrip, events)
	}
}

func TestWriteEvidenceIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evidence")
	first := EvidenceSnapshot{
		Prompt: "first",
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: "first stdout"},
			{Type: coreexec.EventStderr, Data: "first stderr"},
		},
	}
	second := EvidenceSnapshot{
		AgentID: "codex",
		Prompt:  "second",
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: "second stdout"},
		},
	}

	if err := WriteEvidence(dir, first); err != nil {
		t.Fatalf("WriteEvidence(first): %v", err)
	}
	if err := WriteEvidence(dir, second); err != nil {
		t.Fatalf("WriteEvidence(second): %v", err)
	}

	promptBytes, err := os.ReadFile(filepath.Join(dir, "prompt.txt"))
	if err != nil {
		t.Fatalf("ReadFile(prompt.txt): %v", err)
	}
	if got := string(promptBytes); got != second.Prompt {
		t.Fatalf("prompt = %q, want %q", got, second.Prompt)
	}

	assistantBytes, err := os.ReadFile(filepath.Join(dir, "assistant_text.txt"))
	if err != nil {
		t.Fatalf("ReadFile(assistant_text.txt): %v", err)
	}
	if got, want := string(assistantBytes), "second stdout"; got != want {
		t.Fatalf("assistant text = %q, want %q", got, want)
	}

	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(events.jsonl): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != len(second.Events) {
		t.Fatalf("events lines = %d, want %d", len(lines), len(second.Events))
	}
	if strings.Contains(string(data), "first stderr") {
		t.Fatal("events.jsonl retained first write content")
	}

	metaBytes, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		t.Fatalf("ReadFile(meta.json): %v", err)
	}
	var meta struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("Unmarshal(meta.json): %v", err)
	}
	if meta.AgentID != second.AgentID {
		t.Fatalf("meta agent_id = %q, want %q", meta.AgentID, second.AgentID)
	}
}
