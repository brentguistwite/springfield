package workflow_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/planner"
	"springfield/internal/features/workflow"
)

func TestLoadWorkLoadsApprovedSingleWork(t *testing.T) {
	root := t.TempDir()
	writeWorkflowDraft(t, root, workflowDraftFixture{
		workID:  "wave-c2",
		title:   "Unified execution surface",
		summary: "Route approved Springfield work into one execution boundary.",
		split:   "single",
		workstreams: []workflowDraftWorkstream{
			{name: "01", title: "Execution adapter", summary: "Connect one workstream to the single engine."},
		},
	})

	work, err := workflow.LoadWork(root, "wave-c2")
	if err != nil {
		t.Fatalf("LoadWork: %v", err)
	}

	if got, want := work.ID, "wave-c2"; got != want {
		t.Fatalf("work id = %q, want %q", got, want)
	}
	if got, want := work.Title, "Unified execution surface"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	if got, want := work.Split, "single"; got != want {
		t.Fatalf("split = %q, want %q", got, want)
	}
	if got, want := len(work.Workstreams), 1; got != want {
		t.Fatalf("workstreams = %d, want %d", got, want)
	}
	if got, want := work.Workstreams[0].Title, "Execution adapter"; got != want {
		t.Fatalf("workstream title = %q, want %q", got, want)
	}
}

func TestLoadWorkLoadsMultipleApprovedWorkstreams(t *testing.T) {
	root := t.TempDir()
	writeWorkflowDraft(t, root, workflowDraftFixture{
		workID:  "wave-c2",
		title:   "Unified execution surface",
		summary: "Split execution between status and resume follow-up slices.",
		split:   "multi",
		workstreams: []workflowDraftWorkstream{
			{name: "01", title: "Status surface", summary: "Add active-work status output."},
			{name: "02", title: "Resume surface", summary: "Add approved-work resume flow."},
		},
	})

	work, err := workflow.LoadWork(root, "wave-c2")
	if err != nil {
		t.Fatalf("LoadWork: %v", err)
	}

	if got, want := len(work.Workstreams), 2; got != want {
		t.Fatalf("workstreams = %d, want %d", got, want)
	}
	if got, want := work.Workstreams[1].Name, "02"; got != want {
		t.Fatalf("second workstream name = %q, want %q", got, want)
	}
	if got, want := work.Workstreams[1].Summary, "Add approved-work resume flow."; got != want {
		t.Fatalf("second workstream summary = %q, want %q", got, want)
	}
}

func TestLoadWorkRejectsMissingRunState(t *testing.T) {
	root := t.TempDir()
	writeWorkflowDraftFiles(t, root, workflowDraftFixture{
		workID:  "wave-c2",
		title:   "Unified execution surface",
		summary: "Missing run-state should fail fast.",
		split:   "single",
		workstreams: []workflowDraftWorkstream{
			{name: "01", title: "Execution adapter"},
		},
	}, false)

	_, err := workflow.LoadWork(root, "wave-c2")
	if err == nil {
		t.Fatal("expected missing run-state error")
	}
	if !strings.Contains(err.Error(), "run-state") {
		t.Fatalf("expected run-state error, got %v", err)
	}
}

func TestLoadWorkRejectsUnapprovedOrMalformedWork(t *testing.T) {
	tests := []struct {
		name     string
		fixture  workflowDraftFixture
		mutate   func(t *testing.T, root string)
		wantText string
	}{
		{
			name: "unapproved",
			fixture: workflowDraftFixture{
				workID:  "wave-c2",
				title:   "Unified execution surface",
				summary: "Approval gate is required.",
				split:   "single",
				workstreams: []workflowDraftWorkstream{
					{name: "01", title: "Execution adapter"},
				},
				approved:    false,
				approvedSet: true,
			},
			wantText: "approved",
		},
		{
			name: "malformed split",
			fixture: workflowDraftFixture{
				workID:  "wave-c2",
				title:   "Unified execution surface",
				summary: "Split must be valid.",
				split:   "broken",
				workstreams: []workflowDraftWorkstream{
					{name: "01", title: "Execution adapter"},
				},
			},
			wantText: "split",
		},
		{
			name: "missing workstream file",
			fixture: workflowDraftFixture{
				workID:  "wave-c2",
				title:   "Unified execution surface",
				summary: "Referenced workstreams must exist.",
				split:   "single",
				workstreams: []workflowDraftWorkstream{
					{name: "01", title: "Execution adapter"},
				},
			},
			mutate: func(t *testing.T, root string) {
				t.Helper()
				removeWorkflowFile(t, root, "wave-c2", "workstream-01.json")
			},
			wantText: "workstream",
		},
		{
			name: "single split with multiple workstreams",
			fixture: workflowDraftFixture{
				workID:  "wave-c2",
				title:   "Unified execution surface",
				summary: "Single split must stay single.",
				split:   "single",
				workstreams: []workflowDraftWorkstream{
					{name: "01", title: "Execution adapter"},
					{name: "02", title: "Status surface"},
				},
			},
			wantText: "exactly one workstream",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeWorkflowDraft(t, root, tt.fixture)
			if tt.mutate != nil {
				tt.mutate(t, root)
			}

			_, err := workflow.LoadWork(root, "wave-c2")
			if err == nil {
				t.Fatal("expected load error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tt.wantText) {
				t.Fatalf("expected error to contain %q, got %v", tt.wantText, err)
			}
		})
	}
}

func TestWriteDraftSetsActiveWorkID(t *testing.T) {
	root := t.TempDir()

	if err := workflow.WriteDraft(root, workflow.Draft{
		RequestBody: "Polish Wave D1 status UX",
		Response: plannerResponseFixture("wave-d1", "Product polish", "single", []workflowDraftWorkstream{
			{name: "01", title: "Status UX", summary: "Tighten current work resolution."},
		}),
	}); err != nil {
		t.Fatalf("WriteDraft: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, ".springfield", "work", "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}

	var index struct {
		ActiveWorkID string `json:"active_work_id"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	if got, want := index.ActiveWorkID, "wave-d1"; got != want {
		t.Fatalf("active work id = %q, want %q", got, want)
	}
}

func TestCurrentWorkIDPrefersExplicitActiveWorkID(t *testing.T) {
	root := t.TempDir()
	writeWorkflowJSON(t, filepath.Join(root, ".springfield", "work", "index.json"), map[string]any{
		"active_work_id": "wave-c2",
		"works": []map[string]string{
			{
				"id":    "wave-c2",
				"title": "Unified execution surface",
				"split": "single",
			},
			{
				"id":    "wave-d1",
				"title": "Product polish",
				"split": "single",
			},
		},
	})

	workID, err := workflow.CurrentWorkID(root)
	if err != nil {
		t.Fatalf("CurrentWorkID: %v", err)
	}
	if got, want := workID, "wave-c2"; got != want {
		t.Fatalf("work id = %q, want %q", got, want)
	}
}

func TestCurrentWorkIDRejectsMissingExplicitActiveWorkID(t *testing.T) {
	root := t.TempDir()
	writeWorkflowJSON(t, filepath.Join(root, ".springfield", "work", "index.json"), map[string]any{
		"active_work_id": "wave-missing",
		"works": []map[string]string{
			{
				"id":    "wave-c2",
				"title": "Unified execution surface",
				"split": "single",
			},
			{
				"id":    "wave-d1",
				"title": "Product polish",
				"split": "single",
			},
		},
	})

	_, err := workflow.CurrentWorkID(root)
	if err == nil {
		t.Fatal("expected missing active work error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "active") {
		t.Fatalf("expected active work error, got %v", err)
	}
}

type workflowDraftFixture struct {
	workID      string
	title       string
	summary     string
	split       string
	approved    bool
	approvedSet bool
	workstreams []workflowDraftWorkstream
}

type workflowDraftWorkstream struct {
	name    string
	title   string
	summary string
}

func plannerResponseFixture(workID, title, split string, workstreams []workflowDraftWorkstream) planner.Response {
	planned := make([]planner.Workstream, 0, len(workstreams))
	for _, workstream := range workstreams {
		planned = append(planned, planner.Workstream{
			Name:    workstream.name,
			Title:   workstream.title,
			Summary: workstream.summary,
		})
	}

	return planner.Response{
		Mode:        planner.ModeDraft,
		WorkID:      workID,
		Title:       title,
		Summary:     title,
		Split:       planner.Split(split),
		Workstreams: planned,
	}
}

func writeWorkflowDraft(t *testing.T, root string, fixture workflowDraftFixture) {
	t.Helper()
	writeWorkflowDraftFiles(t, root, fixture, true)
}

func writeWorkflowDraftFiles(t *testing.T, root string, fixture workflowDraftFixture, includeRunState bool) {
	t.Helper()

	if !fixture.approvedSet {
		fixture.approved = true
	}

	workDir := filepath.Join(root, ".springfield", "work", fixture.workID)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}

	index := map[string]any{
		"works": []map[string]string{
			{
				"id":    fixture.workID,
				"title": fixture.title,
				"split": fixture.split,
			},
		},
	}
	writeWorkflowJSON(t, filepath.Join(root, ".springfield", "work", "index.json"), index)

	for _, workstream := range fixture.workstreams {
		writeWorkflowJSON(t, filepath.Join(workDir, "workstream-"+workstream.name+".json"), map[string]string{
			"name":    workstream.name,
			"title":   workstream.title,
			"summary": workstream.summary,
		})
	}

	if includeRunState {
		workstreamNames := make([]string, 0, len(fixture.workstreams))
		for _, workstream := range fixture.workstreams {
			workstreamNames = append(workstreamNames, workstream.name)
		}
		writeWorkflowJSON(t, filepath.Join(workDir, "run-state.json"), map[string]any{
			"status":      "draft",
			"approved":    fixture.approved,
			"split":       fixture.split,
			"workstreams": workstreamNames,
		})
	}
}

func writeWorkflowJSON(t *testing.T, path string, value any) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent dir: %v", err)
	}

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write json %s: %v", path, err)
	}
}

func removeWorkflowFile(t *testing.T, root, workID, name string) {
	t.Helper()

	path := filepath.Join(root, ".springfield", "work", workID, name)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove %s: %v", path, err)
	}
}
