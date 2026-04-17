package batch

import (
	"fmt"
	"regexp"
	"strings"
)

// CompileInput carries the raw source for one batch compilation.
type CompileInput struct {
	// Title is derived from the source and used for the batch ID and branch names.
	Title string
	// Source is the raw markdown or prompt text to compile.
	Source string
	// Kind indicates whether Source came from a file or a direct prompt.
	Kind SourceKind
	// ExistingIDs is the set of batch IDs already in use (for collision avoidance).
	ExistingIDs map[string]struct{}
}

// CompileOutput is the result of compiling a batch from source.
type CompileOutput struct {
	Batch  Batch
	Source string // normalized source text persisted alongside batch.json
}

// Compile turns a CompileInput into a ready-to-persist Batch.
func Compile(in CompileInput) (CompileOutput, error) {
	if strings.TrimSpace(in.Source) == "" {
		return CompileOutput{}, fmt.Errorf("source must not be empty")
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = derivedTitle(in.Source)
	}

	existingIDs := in.ExistingIDs
	if existingIDs == nil {
		existingIDs = map[string]struct{}{}
	}

	rawID := SanitizeID(title)
	if rawID == "" {
		rawID = "batch"
	}
	batchID := UniqueID(rawID, existingIDs)

	var slices []Slice
	switch in.Kind {
	case SourceFile:
		slices = parseMarkdownSlices(in.Source)
	default:
		// prompt mode: one slice
		slices = []Slice{
			{ID: "01", Title: strings.TrimSpace(title), Summary: strings.TrimSpace(in.Source), Status: SliceQueued},
		}
	}

	if len(slices) == 0 {
		slices = []Slice{
			{ID: "01", Title: title, Status: SliceQueued},
		}
	}

	sliceIDs := make([]string, 0, len(slices))
	for _, s := range slices {
		sliceIDs = append(sliceIDs, s.ID)
	}

	phases := []Phase{
		{Mode: PhaseSerial, Slices: sliceIDs},
	}

	b := Batch{
		ID:         batchID,
		Title:      title,
		SourceKind: in.Kind,
		Phases:     phases,
		Slices:     slices,
	}

	return CompileOutput{Batch: b, Source: in.Source}, nil
}

// reTaskHeader matches markdown task headers like "## Task 1:" or "## Step 1 —".
// The Task/Step prefix is mandatory so numbered subheadings inside a body
// (e.g. "### 1. Acceptance Criteria") do not split slices.
var reTaskHeader = regexp.MustCompile(`(?m)^#{1,3}\s+(?:Task|Step)\s+(\d+)[.:)\s—–-]+(.*?)$`)

// parseMarkdownSlices extracts ordered slices from a plan markdown document.
// Each slice's Summary is the markdown between its task header and the next
// task header (or end of document), trimmed of surrounding whitespace.
func parseMarkdownSlices(md string) []Slice {
	matches := reTaskHeader.FindAllStringSubmatchIndex(md, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := map[string]struct{}{}
	slices := make([]Slice, 0, len(matches))
	for i, m := range matches {
		num := strings.TrimSpace(md[m[2]:m[3]])
		title := strings.TrimSpace(md[m[4]:m[5]])
		if title == "" {
			title = "Task " + num
		}

		bodyStart := m[1]
		bodyEnd := len(md)
		if i+1 < len(matches) {
			bodyEnd = matches[i+1][0]
		}
		summary := strings.TrimSpace(md[bodyStart:bodyEnd])

		id := fmt.Sprintf("%02s", num)
		id = UniqueID(id, seen)
		seen[id] = struct{}{}

		slices = append(slices, Slice{
			ID:      id,
			Title:   title,
			Summary: summary,
			Status:  SliceQueued,
		})
	}
	return slices
}

// derivedTitle extracts the first H1 from markdown, or uses the first non-empty line.
var reH1 = regexp.MustCompile(`(?m)^#\s+(.+)$`)

func derivedTitle(source string) string {
	if m := reH1.FindStringSubmatch(source); m != nil {
		return strings.TrimSpace(m[1])
	}
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			if len(trimmed) > 60 {
				trimmed = trimmed[:60]
			}
			return trimmed
		}
	}
	return "Springfield batch"
}
