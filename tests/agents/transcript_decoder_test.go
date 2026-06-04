package agents_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	coreexec "springfield/internal/core/exec"
	"springfield/internal/features/conductor/planreview"
	"springfield/internal/testsupport/fixtures"
)

func mustDecoder(t *testing.T, a agents.Adapter) agents.TranscriptDecoder {
	t.Helper()
	d, ok := a.(agents.TranscriptDecoder)
	if !ok {
		t.Fatalf("adapter %T does not implement TranscriptDecoder", a)
	}
	return d
}

// BUG-1 end-to-end: the verdict marker is present and correctly formatted in
// the reviewer's output, but inside the stream-json transport its newlines are
// the literal 2-byte escape, so the anchored (?m)^...$ scan over RAW event data
// never matches. Decoding the assistant text out of the transport first (the
// per-adapter TranscriptDecoder) restores real newlines and the scan succeeds.
// Both fixtures are REAL captures, so this pins the exact production shape.

func TestClaudeDecodeEnablesVerdictScan(t *testing.T) {
	events := fixtures.LoadEvents(t, filepath.Join("..", "realcaptures", "claude", "reviewer-verdict-pass-no-tools.jsonl"))

	// The bug: raw stream-json events do NOT match the anchored verdict regex.
	if _, found := planreview.ScanReviewVerdict(events); found {
		t.Fatal("raw stream-json must NOT match the anchored verdict regex (this is BUG-1)")
	}

	text := mustDecoder(t, claude.New(exec.LookPath)).AssistantText(events)
	if !strings.Contains(text, "<review-verdict>pass</review-verdict>") {
		t.Fatalf("decoded text missing verdict marker: %q", text)
	}

	// The fix: scanning the decoded text finds the verdict cleanly.
	v, found := planreview.ScanReviewVerdict([]coreexec.Event{{Type: coreexec.EventStdout, Data: text}})
	if !found || v.Class != planreview.VerdictPass {
		t.Fatalf("decoded scan: found=%v class=%q, want found=true pass", found, v.Class)
	}
}

func TestCodexDecodeEnablesVerdictScan(t *testing.T) {
	events := fixtures.LoadEvents(t, filepath.Join("..", "realcaptures", "codex", "reviewer-verdict-pass-no-tools.jsonl"))

	if _, found := planreview.ScanReviewVerdict(events); found {
		t.Fatal("raw codex stream-json must NOT match the anchored verdict regex (this is BUG-1)")
	}

	text := mustDecoder(t, codex.New(exec.LookPath)).AssistantText(events)
	if !strings.Contains(text, "<review-verdict>pass</review-verdict>") {
		t.Fatalf("decoded text missing verdict marker: %q", text)
	}

	v, found := planreview.ScanReviewVerdict([]coreexec.Event{{Type: coreexec.EventStdout, Data: text}})
	if !found || v.Class != planreview.VerdictPass {
		t.Fatalf("decoded scan: found=%v class=%q, want found=true pass", found, v.Class)
	}
}
