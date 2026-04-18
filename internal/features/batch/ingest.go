package batch

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// SlicePayload is the stdin/file JSON shape for --slices ingest.
// Callers (typically the springfield:plan skill) emit this instead of relying
// on markdown regex parsing inside the engine.
type SlicePayload struct {
	Title  string         `json:"title"`
	Source string         `json:"source"`
	Slices []SliceRequest `json:"slices"`
}

// SliceRequest is one caller-provided slice before Compile assigns status/IDs.
type SliceRequest struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
}

// ParseSlicePayload decodes and validates a SlicePayload from r.
func ParseSlicePayload(r io.Reader) (SlicePayload, error) {
	var p SlicePayload
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return SlicePayload{}, fmt.Errorf("decode slice payload: %w", err)
	}
	if strings.TrimSpace(p.Title) == "" {
		return SlicePayload{}, fmt.Errorf("slice payload: title required")
	}
	if strings.TrimSpace(p.Source) == "" {
		return SlicePayload{}, fmt.Errorf("slice payload: source required")
	}
	if len(p.Slices) == 0 {
		return SlicePayload{}, fmt.Errorf("slice payload: at least one slice required")
	}
	seen := map[string]struct{}{}
	for i, s := range p.Slices {
		if strings.TrimSpace(s.ID) == "" {
			return SlicePayload{}, fmt.Errorf("slice payload: slice %d missing id", i)
		}
		if strings.TrimSpace(s.Title) == "" {
			return SlicePayload{}, fmt.Errorf("slice payload: slice %q missing title", s.ID)
		}
		if _, dup := seen[s.ID]; dup {
			return SlicePayload{}, fmt.Errorf("slice payload: duplicate slice id %q", s.ID)
		}
		seen[s.ID] = struct{}{}
	}
	return p, nil
}
