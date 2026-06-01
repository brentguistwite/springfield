package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

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

func TestEmitClaudeBillingWarning_NoClaude(t *testing.T) {
	var buf bytes.Buffer
	if emitClaudeBillingWarning(&buf, t.TempDir(), []string{"codex"}) {
		t.Fatal("expected no warning when claude not in priority")
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output, got %q", buf.String())
	}
}

func TestEmitClaudeBillingWarning_NoHistory(t *testing.T) {
	var buf bytes.Buffer
	if !emitClaudeBillingWarning(&buf, t.TempDir(), []string{"claude", "codex"}) {
		t.Fatal("expected warning to fire with claude in priority")
	}
	out := buf.String()
	if !regexp.MustCompile(`claude is in agent_priority`).MatchString(out) {
		t.Errorf("missing warning header: %q", out)
	}
	if !regexp.MustCompile(`no prior batches`).MatchString(out) {
		t.Errorf("missing no-history hint: %q", out)
	}
	if regexp.MustCompile(`~\$\d+\.\d{2}`).MatchString(out) {
		t.Errorf("expected no $ range with empty history: %q", out)
	}
}

func TestEmitClaudeBillingWarning_WithArchive(t *testing.T) {
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
