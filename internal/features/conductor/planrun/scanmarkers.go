package planrun

import (
	"regexp"
	"strings"

	coreexec "springfield/internal/core/exec"
)

// storyPassRe matches the full <story-pass>US-NNN</story-pass> marker and
// captures the complete "US-NNN" ID (with the "US-" prefix) so callers can
// pass it directly to MarkPassed.
var storyPassRe = regexp.MustCompile(`<story-pass>(US-\d+)</story-pass>`)

// completeMarker is the exact literal that signals plan completion.
const completeMarker = "<promise>COMPLETE</promise>"

// ScanMarkers extracts story-pass and complete markers from agent output.
//
// For each event with Type == EventStdout, scans Event.Data for:
//   - the literal "<promise>COMPLETE</promise>"
//   - matches of "<story-pass>US-(\d+)</story-pass>"
//
// Story IDs returned are deduplicated, preserving first-occurrence order
// across all stdout events.
//
// Scan target is raw Event.Data (the entire stdout line). Adapters that
// wrap output in JSON have their JSON line text-searched: marker tags are
// unusual enough that incidental collisions are vanishingly unlikely.
// False-positive risk is accepted in v1 in exchange for adapter-agnostic
// scanning. Promote to JSON-aware extraction if a real adapter trips it.
//
// Markers split across two events do NOT match — only whole-event scanning
// is performed (regression test: TestScanMarkersMarkerSplitAcrossTwoEventsNotMatched).
func ScanMarkers(events []coreexec.Event) (passedStories []string, complete bool) {
	seen := make(map[string]bool)
	for _, ev := range events {
		if ev.Type != coreexec.EventStdout {
			continue
		}
		if strings.Contains(ev.Data, completeMarker) {
			complete = true
		}
		for _, match := range storyPassRe.FindAllStringSubmatch(ev.Data, -1) {
			id := match[1] // "US-NNN"
			if !seen[id] {
				seen[id] = true
				passedStories = append(passedStories, id)
			}
		}
	}
	return passedStories, complete
}
