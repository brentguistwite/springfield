package workflow

import (
	"encoding/json"
	"fmt"
	"os"

	"springfield/internal/features/planner"
	"springfield/internal/storage"
)

// Work is the Springfield-owned execution input loaded from persisted work state.
type Work struct {
	ID          string
	Title       string
	RequestBody string
	Split       string
	Workstreams []Workstream
}

// Workstream is one approved Springfield workstream ready for execution.
type Workstream struct {
	Name         string
	Title        string
	Summary      string
	Status       string
	Error        string
	EvidencePath string
}

// LoadWork reads one approved Springfield work item from .springfield/work.
func LoadWork(root, workID string) (Work, error) {
	rt, err := storage.FromRoot(root)
	if err != nil {
		return Work{}, fmt.Errorf("resolve runtime: %w", err)
	}

	index, err := readIndex(rt.WorkIndexPath())
	if err != nil {
		return Work{}, err
	}

	entry, ok := findIndexEntry(index, workID)
	if !ok {
		return Work{}, fmt.Errorf("work %q not found in Springfield index", workID)
	}

	workPaths, err := rt.Work(workID)
	if err != nil {
		return Work{}, fmt.Errorf("resolve work paths: %w", err)
	}

	state, err := readRunState(workPaths.RunStatePath())
	if err != nil {
		return Work{}, err
	}
	if !state.Approved {
		return Work{}, fmt.Errorf("work %q is not approved", workID)
	}
	if !validSplit(state.Split) {
		return Work{}, fmt.Errorf("work %q has unsupported split %q", workID, state.Split)
	}
	if entry.Split != "" && entry.Split != state.Split {
		return Work{}, fmt.Errorf("work %q split mismatch: index=%q run-state=%q", workID, entry.Split, state.Split)
	}
	if len(state.Workstreams) == 0 {
		return Work{}, fmt.Errorf("work %q run-state lists no workstreams", workID)
	}

	requestBody := ""
	if data, err := os.ReadFile(workPaths.RequestPath()); err == nil {
		requestBody = string(data)
	} else if !os.IsNotExist(err) {
		return Work{}, fmt.Errorf("read request %s: %w", workPaths.RequestPath(), err)
	}

	workstreams := make([]Workstream, 0, len(state.Workstreams))
	workstreamState := workstreamStateByName(state)
	for _, name := range state.Workstreams {
		file, err := readWorkstreamFile(workPaths.WorkstreamPath(name))
		if err != nil {
			return Work{}, err
		}
		if file.Title == "" {
			return Work{}, fmt.Errorf("workstream %q is missing a title", name)
		}

		workstreams = append(workstreams, Workstream{
			Name:         file.Name,
			Title:        file.Title,
			Summary:      file.Summary,
			Status:       currentWorkstreamStatus(name, state, workstreamState),
			Error:        workstreamState[name].Error,
			EvidencePath: workstreamState[name].EvidencePath,
		})
	}

	return Work{
		ID:          entry.ID,
		Title:       entry.Title,
		RequestBody: requestBody,
		Split:       state.Split,
		Workstreams: workstreams,
	}, nil
}

// CurrentWorkID resolves the most recently indexed Springfield work item.
func CurrentWorkID(root string) (string, error) {
	rt, err := storage.FromRoot(root)
	if err != nil {
		return "", fmt.Errorf("resolve runtime: %w", err)
	}

	index, err := readIndex(rt.WorkIndexPath())
	if err != nil {
		return "", err
	}
	if len(index.Works) == 0 {
		return "", fmt.Errorf("no Springfield work is available yet")
	}

	return index.Works[len(index.Works)-1].ID, nil
}

func findIndexEntry(index workIndex, workID string) (workIndexEntry, bool) {
	for _, entry := range index.Works {
		if entry.ID == workID {
			return entry, true
		}
	}
	return workIndexEntry{}, false
}

func readRunState(path string) (runStateFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return runStateFile{}, fmt.Errorf("read run-state %s: %w", path, err)
		}
		return runStateFile{}, fmt.Errorf("read run-state %s: %w", path, err)
	}

	var state runStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return runStateFile{}, fmt.Errorf("decode run-state %s: %w", path, err)
	}
	return state, nil
}

func readWorkstreamFile(path string) (workstreamFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return workstreamFile{}, fmt.Errorf("read workstream %s: %w", path, err)
	}

	var file workstreamFile
	if err := json.Unmarshal(data, &file); err != nil {
		return workstreamFile{}, fmt.Errorf("decode workstream %s: %w", path, err)
	}
	if file.Name == "" {
		return workstreamFile{}, fmt.Errorf("decode workstream %s: missing name", path)
	}

	return file, nil
}

func validSplit(split string) bool {
	return split == string(planner.SplitSingle) || split == string(planner.SplitMulti)
}

func workstreamStateByName(state runStateFile) map[string]workstreamStatusFile {
	byName := make(map[string]workstreamStatusFile, len(state.WorkstreamStates))
	for _, workstream := range state.WorkstreamStates {
		if workstream.Name == "" {
			continue
		}
		byName[workstream.Name] = workstream
	}
	return byName
}

func currentWorkstreamStatus(name string, state runStateFile, byName map[string]workstreamStatusFile) string {
	if workstream, ok := byName[name]; ok && workstream.Status != "" {
		return workstream.Status
	}
	if state.Approved {
		return "ready"
	}
	return ""
}
