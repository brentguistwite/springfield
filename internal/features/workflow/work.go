package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"springfield/internal/features/planner"
	"springfield/internal/storage"
)

// Draft is the approved planner output Springfield persists to work state.
type Draft struct {
	RequestBody string
	Response    planner.Response
}

type workIndex struct {
	Works []workIndexEntry `json:"works"`
}

type workIndexEntry struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Split string `json:"split"`
}

type workstreamFile struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
}

type runStateFile struct {
	Status           string                 `json:"status"`
	Approved         bool                   `json:"approved"`
	Split            string                 `json:"split"`
	Workstreams      []string               `json:"workstreams"`
	Error            string                 `json:"error,omitempty"`
	WorkstreamStates []workstreamStatusFile `json:"workstream_states,omitempty"`
}

type workstreamStatusFile struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	EvidencePath string `json:"evidence_path,omitempty"`
}

// WriteDraft persists one approved planning draft under .springfield/work.
func WriteDraft(root string, draft Draft) error {
	if err := planner.Validate(draft.Response); err != nil {
		return fmt.Errorf("validate draft response: %w", err)
	}

	rt, err := storage.FromRoot(root)
	if err != nil {
		return fmt.Errorf("resolve runtime: %w", err)
	}
	if err := rt.Ensure(); err != nil {
		return err
	}

	work, err := rt.Work(draft.Response.WorkID)
	if err != nil {
		return fmt.Errorf("resolve work paths: %w", err)
	}
	if err := os.MkdirAll(work.DirPath(), 0o755); err != nil {
		return fmt.Errorf("create work dir %s: %w", work.DirPath(), err)
	}

	if err := os.WriteFile(work.RequestPath(), []byte(draft.RequestBody), 0o644); err != nil {
		return fmt.Errorf("write request %s: %w", work.RequestPath(), err)
	}

	workstreamNames := make([]string, 0, len(draft.Response.Workstreams))
	for _, workstream := range draft.Response.Workstreams {
		workstreamNames = append(workstreamNames, workstream.Name)
		if err := writeJSONFile(work.WorkstreamPath(workstream.Name), workstreamFile{
			Name:    workstream.Name,
			Title:   workstream.Title,
			Summary: workstream.Summary,
		}); err != nil {
			return err
		}
	}

	if err := writeJSONFile(work.RunStatePath(), runStateFile{
		Status:      "draft",
		Approved:    true,
		Split:       string(draft.Response.Split),
		Workstreams: workstreamNames,
	}); err != nil {
		return err
	}

	index, err := readIndex(rt.WorkIndexPath())
	if err != nil {
		return err
	}
	index.upsert(workIndexEntry{
		ID:    draft.Response.WorkID,
		Title: draft.Response.Title,
		Split: string(draft.Response.Split),
	})

	if err := writeJSONFile(rt.WorkIndexPath(), index); err != nil {
		return err
	}

	return nil
}

func readIndex(path string) (workIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return workIndex{}, nil
		}
		return workIndex{}, fmt.Errorf("read work index %s: %w", path, err)
	}

	var index workIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return workIndex{}, fmt.Errorf("decode work index %s: %w", path, err)
	}
	return index, nil
}

func (index *workIndex) upsert(entry workIndexEntry) {
	for i := range index.Works {
		if index.Works[i].ID == entry.ID {
			index.Works[i] = entry
			return
		}
	}
	index.Works = append(index.Works, entry)
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent dir for %s: %w", path, err)
	}

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode json %s: %w", path, err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write json %s: %w", path, err)
	}
	return nil
}
