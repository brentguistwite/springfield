package batch

import (
	"fmt"
	"strings"
)

// CompileInput carries the raw source + caller-provided slices for one batch.
type CompileInput struct {
	Title       string
	Source      string
	Slices      []SliceRequest
	ExistingIDs map[string]struct{}
}

// CompileOutput is the result of compiling a batch from a slice payload.
type CompileOutput struct {
	Batch  Batch
	Source string
}

// Compile turns a CompileInput into a ready-to-persist Batch. Caller owns
// slice boundaries — no markdown parsing happens here.
func Compile(in CompileInput) (CompileOutput, error) {
	if strings.TrimSpace(in.Source) == "" {
		return CompileOutput{}, fmt.Errorf("source must not be empty")
	}
	if strings.TrimSpace(in.Title) == "" {
		return CompileOutput{}, fmt.Errorf("title must not be empty")
	}
	if len(in.Slices) == 0 {
		return CompileOutput{}, fmt.Errorf("at least one slice required")
	}

	title := strings.TrimSpace(in.Title)

	existingIDs := in.ExistingIDs
	if existingIDs == nil {
		existingIDs = map[string]struct{}{}
	}
	rawID := SanitizeID(title)
	if rawID == "" {
		rawID = "batch"
	}
	batchID := UniqueID(rawID, existingIDs)

	// Slice IDs arrive from a caller (skill/tool) and are persisted + echoed in
	// operator-facing output AND embedded into downstream work IDs. Never trust
	// them raw: canonicalize via SanitizeID, fall back to a positional ID if
	// nothing survives, then dedupe.
	seen := map[string]struct{}{}
	slices := make([]Slice, 0, len(in.Slices))
	ids := make([]string, 0, len(in.Slices))
	for i, r := range in.Slices {
		id := SanitizeID(r.ID)
		if id == "" {
			id = fmt.Sprintf("%02d", i+1)
		}
		id = UniqueID(id, seen)
		seen[id] = struct{}{}
		slices = append(slices, Slice{
			ID:      id,
			Title:   strings.TrimSpace(r.Title),
			Summary: strings.TrimSpace(r.Summary),
			Status:  SliceQueued,
		})
		ids = append(ids, id)
	}

	return CompileOutput{
		Batch: Batch{
			ID:     batchID,
			Title:  title,
			Phases: []Phase{{Mode: PhaseSerial, Slices: ids}},
			Slices: slices,
		},
		Source: in.Source,
	}, nil
}
