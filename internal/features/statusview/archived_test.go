package statusview_test

import (
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/statusview"
)

// TestArchivedPlanParityWithLive locks the contract that a per-ticket card
// projected from an archived record has the SAME shape and core fields as the
// live PlanView it was snapshotted from. A completed plan with no merge ledger
// is "merged" both live and archived — the legacy nil-Merge path treats it as
// integrated (ComposeStatus → merged; statusFromArchiveStatus mode="" → merged)
// — exact parity on the round-tripping fields.
func TestArchivedPlanParityWithLive(t *testing.T) {
	ps := &conductor.PlanState{
		Status:       conductor.StatusCompleted,
		Branch:       "springfield/alpha",
		BaseRef:      "develop",
		EvidencePath: ".springfield/archive/batch-1/plans/alpha",
	}
	live := statusview.BuildPlanForTest("alpha", "Alpha", ps, false)

	// The archive record FinalizeBatch would write for this plan.
	ap := batch.ArchivePlan{
		ID:           "alpha",
		Title:        "Alpha",
		Status:       string(conductor.StatusCompleted),
		Branch:       "springfield/alpha",
		BaseRef:      "develop",
		EvidencePath: ".springfield/archive/batch-1/plans/alpha",
	}
	archived := statusview.PlanFromArchive(ap, "")

	if archived.ID != live.ID || archived.Title != live.Title {
		t.Fatalf("identity mismatch: live=%+v archived=%+v", live, archived)
	}
	if archived.Branch != live.Branch || archived.BaseBranch != live.BaseBranch {
		t.Fatalf("branch/base mismatch: live=%+v archived=%+v", live, archived)
	}
	if archived.EvidencePath != live.EvidencePath {
		t.Fatalf("evidence mismatch: live=%q archived=%q", live.EvidencePath, archived.EvidencePath)
	}
	if archived.Status != live.Status {
		t.Fatalf("status parity: live=%q archived=%q", live.Status, archived.Status)
	}
}

func TestArchivedViewProjectsEntry(t *testing.T) {
	entry := batch.ArchiveEntry{
		BatchID:       "batch-1",
		Title:         "Test Batch",
		TotalUSD:      1.23,
		CostBreakdown: map[string]float64{"claude": 1.23},
		Plans: []batch.ArchivePlan{
			{ID: "alpha", Title: "Alpha", Status: "completed", Branch: "springfield/alpha", BaseRef: "develop"},
			{ID: "beta", Title: "Beta", Status: "completed", Branch: "springfield/beta", BaseRef: "develop"},
		},
	}
	v := statusview.Archived(entry, "")

	if v.State != "archived" {
		t.Fatalf("state = %q, want archived", v.State)
	}
	if v.Batch == nil || v.Batch.ID != "batch-1" {
		t.Fatalf("batch view wrong: %+v", v.Batch)
	}
	if len(v.Plans) != 2 {
		t.Fatalf("want 2 plans, got %d", len(v.Plans))
	}
	if v.Spend == nil || v.Spend.TotalUSD != 1.23 {
		t.Fatalf("spend not projected: %+v", v.Spend)
	}
	if v.SchemaVersion != statusview.SchemaVersion {
		t.Fatalf("schema version unset")
	}
}
