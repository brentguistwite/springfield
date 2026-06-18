package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"springfield/internal/core/agents"
)

// enableClaudeMetering flips the master switch on for the duration of the test
// so the metered-path billing warning can be exercised. The shipped default is
// off — claude is not separately metered, so the warning never fires (see
// TestEmitClaudeBillingWarning_NotMeteredNeverFires).
func enableClaudeMetering(t *testing.T) {
	t.Helper()
	prev := agents.ClaudeHeadlessMetered
	agents.ClaudeHeadlessMetered = true
	t.Cleanup(func() { agents.ClaudeHeadlessMetered = prev })
}

func writeArchive(t *testing.T, root, batchID string, totalUSD float64, planCount int) {
	t.Helper()
	dir := filepath.Join(root, ".springfield", "archive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	plans := make([]map[string]string, planCount)
	for i := range plans {
		plans[i] = map[string]string{"id": "p", "title": "t", "status": "completed"}
	}
	entry := map[string]any{
		"batch_id":    batchID,
		"title":       batchID,
		"archived_at": time.Now().UTC().Format(time.RFC3339Nano),
		"reason":      "completed",
		"plans":       plans,
		"total_usd":   totalUSD,
	}
	data, _ := json.MarshalIndent(entry, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, batchID+".json"), data, 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
}

// TestEmitClaudeBillingWarning_NotMeteredNeverFires pins the shipped default:
// while ClaudeHeadlessMetered is false, the warning never fires even with
// claude in agent_priority and prior cost history present.
func TestEmitClaudeBillingWarning_NotMeteredNeverFires(t *testing.T) {
	if agents.ClaudeHeadlessMetered {
		t.Fatalf("ClaudeHeadlessMetered must default to false")
	}
	root := t.TempDir()
	writeArchive(t, root, "b1", 1.0, 2)
	var buf bytes.Buffer
	if emitClaudeBillingWarning(&buf, root, []string{"claude", "codex"}) {
		t.Fatal("expected no warning while claude headless is not separately metered")
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output when not metered, got %q", buf.String())
	}
}

func TestEmitClaudeBillingWarning_NoClaude(t *testing.T) {
	enableClaudeMetering(t)
	var buf bytes.Buffer
	if emitClaudeBillingWarning(&buf, t.TempDir(), []string{"codex"}) {
		t.Fatal("expected no warning when claude not in priority")
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output, got %q", buf.String())
	}
}

func TestEmitClaudeBillingWarning_NoHistory(t *testing.T) {
	enableClaudeMetering(t)
	var buf bytes.Buffer
	if !emitClaudeBillingWarning(&buf, t.TempDir(), []string{"claude", "codex"}) {
		t.Fatal("expected warning to fire with claude in priority")
	}
	out := buf.String()
	if !regexp.MustCompile(`claude is in agent_priority`).MatchString(out) {
		t.Errorf("missing warning header: %q", out)
	}
	// Pin a phrase from the warning body so the metered-path prose can't be
	// silently reworded/dropped (the body is only reachable when the switch
	// is on, so nothing else guards it).
	if !regexp.MustCompile(`currently metered separately`).MatchString(out) {
		t.Errorf("missing metered-policy body line: %q", out)
	}
	if !regexp.MustCompile(`no prior batches`).MatchString(out) {
		t.Errorf("missing no-history hint: %q", out)
	}
	if regexp.MustCompile(`~\$\d+\.\d{2}`).MatchString(out) {
		t.Errorf("expected no $ range with empty history: %q", out)
	}
}

func TestEmitClaudeBillingWarning_WithArchive(t *testing.T) {
	enableClaudeMetering(t)
	root := t.TempDir()
	writeArchive(t, root, "b1", 1.0, 2)
	writeArchive(t, root, "b2", 0.5, 2)

	var buf bytes.Buffer
	emitClaudeBillingWarning(&buf, root, []string{"claude"})
	out := buf.String()
	rangeRE := regexp.MustCompile(`~\$\d+\.\d{2}–\$\d+\.\d{2}`)
	if !rangeRE.MatchString(out) {
		t.Errorf("expected $ range with archive history, got: %q", out)
	}
}

func TestEmitClaudeBillingWarning_Suppressed(t *testing.T) {
	enableClaudeMetering(t)
	t.Setenv(suppressClaudeBillingWarningEnv, "1")
	var buf bytes.Buffer
	if emitClaudeBillingWarning(&buf, t.TempDir(), []string{"claude"}) {
		t.Fatal("expected suppression env to silence warning")
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output when suppressed, got %q", buf.String())
	}
}

func TestEmitClaudeBillingWarning_LegacyArchivesIgnored(t *testing.T) {
	enableClaudeMetering(t)
	root := t.TempDir()
	// Archive entries with TotalUSD == 0 (pre-PR) must NOT count as $0 batches
	writeArchive(t, root, "legacy-1", 0, 5)
	writeArchive(t, root, "legacy-2", 0, 5)
	var buf bytes.Buffer
	emitClaudeBillingWarning(&buf, root, []string{"claude"})
	out := buf.String()
	if !regexp.MustCompile(`no prior batches`).MatchString(out) {
		t.Errorf("expected no-history hint when only legacy entries present, got: %q", out)
	}
}
