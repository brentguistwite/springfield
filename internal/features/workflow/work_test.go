package workflow_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/features/planner"
	"springfield/internal/features/workflow"
)

type indexFile struct {
	Works []indexEntry `json:"works"`
}

type indexEntry struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Split string `json:"split"`
}

func readIndex(t *testing.T, path string) indexFile {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}

	var file indexFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("decode index: %v", err)
	}

	return file
}

func TestWriteDraftWritesApprovedSingleDraft(t *testing.T) {
	root := t.TempDir()
	draft := workflow.Draft{
		RequestBody: "Add the Wave B planning surface.",
		Response: planner.Response{
			Mode:    planner.ModeDraft,
			WorkID:  "wave-b",
			Title:   "Wave B planning surface",
			Summary: "Keep planning and review in Springfield.",
			Split:   planner.SplitSingle,
			Workstreams: []planner.Workstream{
				{
					Name:    "01",
					Title:   "Implement Wave B",
					Summary: "One workstream.",
				},
			},
		},
	}

	if err := workflow.WriteDraft(root, draft); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	requestPath := filepath.Join(root, ".springfield", "work", "wave-b", "request.md")
	requestBody, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("read request.md: %v", err)
	}
	if got := string(requestBody); got != draft.RequestBody {
		t.Fatalf("request body = %q", got)
	}

	workstreamPath := filepath.Join(root, ".springfield", "work", "wave-b", "workstream-01.json")
	if _, err := os.Stat(workstreamPath); err != nil {
		t.Fatalf("stat workstream file: %v", err)
	}

	runStatePath := filepath.Join(root, ".springfield", "work", "wave-b", "run-state.json")
	if _, err := os.Stat(runStatePath); err != nil {
		t.Fatalf("stat run-state file: %v", err)
	}

	index := readIndex(t, filepath.Join(root, ".springfield", "work", "index.json"))
	if got, want := len(index.Works), 1; got != want {
		t.Fatalf("index entries = %d, want %d", got, want)
	}
	if index.Works[0].ID != "wave-b" {
		t.Fatalf("index work id = %q", index.Works[0].ID)
	}
}

func TestWriteDraftWritesMultipleWorkstreamsForMultiSplit(t *testing.T) {
	root := t.TempDir()
	draft := workflow.Draft{
		RequestBody: "Split Wave B into core and UI workstreams.",
		Response: planner.Response{
			Mode:    planner.ModeDraft,
			WorkID:  "wave-b",
			Title:   "Wave B planning surface",
			Summary: "Split planner core and review UI.",
			Split:   planner.SplitMulti,
			Workstreams: []planner.Workstream{
				{Name: "01", Title: "Planner core"},
				{Name: "02", Title: "Review UI"},
			},
		},
	}

	if err := workflow.WriteDraft(root, draft); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	for _, name := range []string{"01", "02"} {
		path := filepath.Join(root, ".springfield", "work", "wave-b", "workstream-"+name+".json")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat workstream %s: %v", name, err)
		}
	}
}

func TestWriteDraftUpdatesExistingIndexEntry(t *testing.T) {
	root := t.TempDir()
	first := workflow.Draft{
		RequestBody: "First request.",
		Response: planner.Response{
			Mode:    planner.ModeDraft,
			WorkID:  "wave-b",
			Title:   "Old title",
			Summary: "Old summary.",
			Split:   planner.SplitSingle,
			Workstreams: []planner.Workstream{
				{Name: "01", Title: "Initial workstream"},
			},
		},
	}
	second := workflow.Draft{
		RequestBody: "Second request.",
		Response: planner.Response{
			Mode:    planner.ModeDraft,
			WorkID:  "wave-b",
			Title:   "New title",
			Summary: "New summary.",
			Split:   planner.SplitMulti,
			Workstreams: []planner.Workstream{
				{Name: "01", Title: "Planner core"},
				{Name: "02", Title: "Review UI"},
			},
		},
	}

	if err := workflow.WriteDraft(root, first); err != nil {
		t.Fatalf("write first draft: %v", err)
	}
	if err := workflow.WriteDraft(root, second); err != nil {
		t.Fatalf("write second draft: %v", err)
	}

	index := readIndex(t, filepath.Join(root, ".springfield", "work", "index.json"))
	if got, want := len(index.Works), 1; got != want {
		t.Fatalf("index entries = %d, want %d", got, want)
	}
	if got := index.Works[0].Title; got != "New title" {
		t.Fatalf("index title = %q", got)
	}
	if got := index.Works[0].Split; got != string(planner.SplitMulti) {
		t.Fatalf("index split = %q", got)
	}
}
