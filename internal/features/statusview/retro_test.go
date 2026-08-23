package statusview_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/features/batch"
	"springfield/internal/features/retro"
	"springfield/internal/features/statusview"
)

// writeFixtureRetro lays a retro.json down in the archive dir a batch id resolves
// to (.springfield/archive/<batchID>/), mirroring where retro.Persist writes it
// on the completion path.
func writeFixtureRetro(t *testing.T, root, batchID string, findings []retro.Finding) {
	t.Helper()
	batchDir := filepath.Join(batch.ArchiveDir(root), batchID)
	if err := retro.WriteReport(batchDir, &retro.Report{BatchID: batchID, Findings: findings}); err != nil {
		t.Fatalf("write fixture retro.json: %v", err)
	}
}

func archiveEntry() batch.ArchiveEntry {
	return batch.ArchiveEntry{
		BatchID: "batch-1",
		Title:   "Test Batch",
		Plans: []batch.ArchivePlan{
			{ID: "US-001", Title: "One", Status: "completed"},
		},
	}
}

// TestArchivedRetro_JSONIncludesSummary pins that an archived batch with a
// readable retro.json surfaces the compact digest in the view-model: total
// findings plus the top pattern (widest plan spread) and its count.
func TestArchivedRetro_JSONIncludesSummary(t *testing.T) {
	root := t.TempDir()
	writeFixtureRetro(t, root, "batch-1", []retro.Finding{
		{PatternKey: "iteration-cap", Severity: "warning", PlanIDs: []string{"US-003"}},
		{PatternKey: "verify-nonconvergence", Severity: "warning", PlanIDs: []string{"US-001", "US-002"}},
	})

	v := statusview.Archived(archiveEntry(), root)
	if v.Retro == nil {
		t.Fatal("expected Retro summary for archive carrying retro.json")
	}
	if v.Retro.Findings != 2 {
		t.Fatalf("Findings = %d, want 2", v.Retro.Findings)
	}
	if v.Retro.TopPattern != "verify-nonconvergence" || v.Retro.TopCount != 2 {
		t.Fatalf("top = %q x%d, want verify-nonconvergence x2", v.Retro.TopPattern, v.Retro.TopCount)
	}
}

// TestArchivedRetro_AbsentRendersNothing confirms an archive with no retro.json
// leaves Retro null — no extra field, no error.
func TestArchivedRetro_AbsentRendersNothing(t *testing.T) {
	v := statusview.Archived(archiveEntry(), t.TempDir())
	if v.Retro != nil {
		t.Fatalf("expected nil Retro for archive without retro.json, got %+v", v.Retro)
	}
}

// TestArchivedRetro_JSONKeyOmittedWhenAbsent pins the --json contract: with no
// readable digest the `retro` key is omitted entirely (not emitted as null), and
// with a digest the key appears as the documented {findings, top_pattern,
// top_count} object. AC US-002: "includes a retro object ... and omits it
// otherwise."
func TestArchivedRetro_JSONKeyOmittedWhenAbsent(t *testing.T) {
	absent, err := json.Marshal(statusview.Archived(archiveEntry(), t.TempDir()))
	if err != nil {
		t.Fatalf("marshal absent view: %v", err)
	}
	if strings.Contains(string(absent), `"retro"`) {
		t.Fatalf("expected retro key omitted when absent, got %s", absent)
	}

	root := t.TempDir()
	writeFixtureRetro(t, root, "batch-1", []retro.Finding{
		{PatternKey: "verify-nonconvergence", PlanIDs: []string{"US-001", "US-002"}},
	})
	present, err := json.Marshal(statusview.Archived(archiveEntry(), root))
	if err != nil {
		t.Fatalf("marshal present view: %v", err)
	}
	if !strings.Contains(string(present), `"retro"`) {
		t.Fatalf("expected retro key present with a digest, got %s", present)
	}
}

// TestArchivedRetro_CorruptRendersNothing confirms a present-but-corrupt
// retro.json is swallowed: the archived view still projects, just without a retro
// digest.
func TestArchivedRetro_CorruptRendersNothing(t *testing.T) {
	root := t.TempDir()
	batchDir := filepath.Join(batch.ArchiveDir(root), "batch-1")
	if err := os.MkdirAll(batchDir, 0o755); err != nil {
		t.Fatalf("mkdir batchDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(batchDir, "retro.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt retro.json: %v", err)
	}

	v := statusview.Archived(archiveEntry(), root)
	if v.State != "archived" {
		t.Fatalf("state = %q, want archived", v.State)
	}
	if v.Retro != nil {
		t.Fatalf("expected nil Retro for corrupt retro.json, got %+v", v.Retro)
	}
}

// TestArchivedRetro_NoFindingsRendersNothing confirms a clean batch (retro.json
// present but findings-less) surfaces no digest — nothing to report.
func TestArchivedRetro_NoFindingsRendersNothing(t *testing.T) {
	root := t.TempDir()
	writeFixtureRetro(t, root, "batch-1", nil)
	if v := statusview.Archived(archiveEntry(), root); v.Retro != nil {
		t.Fatalf("expected nil Retro for findings-less report, got %+v", v.Retro)
	}
}

// TestRenderRetro_HumanOneLiner pins the human one-line summary naming the top
// pattern key and count.
func TestRenderRetro_HumanOneLiner(t *testing.T) {
	root := t.TempDir()
	writeFixtureRetro(t, root, "batch-1", []retro.Finding{
		{PatternKey: "iteration-cap", PlanIDs: []string{"US-003"}},
		{PatternKey: "verify-nonconvergence", PlanIDs: []string{"US-001", "US-002"}},
	})

	out := statusview.Render(statusview.Archived(archiveEntry(), root), time.Now())
	want := "retro: 2 findings (top: verify-nonconvergence x2)"
	if !strings.Contains(out, want) {
		t.Fatalf("human render missing retro line %q\n--- got ---\n%s", want, out)
	}
}

// TestRenderRetro_AbsentOmitsLine confirms a view with no retro digest renders no
// retro line at all.
func TestRenderRetro_AbsentOmitsLine(t *testing.T) {
	out := statusview.Render(statusview.Archived(archiveEntry(), t.TempDir()), time.Now())
	if strings.Contains(out, "retro:") {
		t.Fatalf("expected no retro line without a retro digest\n--- got ---\n%s", out)
	}
}
