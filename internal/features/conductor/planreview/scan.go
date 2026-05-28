package planreview

import (
	"regexp"
	"strings"

	coreexec "springfield/internal/core/exec"
)

var reviewVerdictRe = regexp.MustCompile(`<review-verdict>(pass|revise|halt)</review-verdict>`)

var severityRank = map[string]int{"pass": 0, "revise": 1, "halt": 2}
var classBySeverity = []VerdictClass{VerdictPass, VerdictRevise, VerdictHalt}

// ScanReviewVerdict extracts the reviewer's verdict from agent stdout. It mirrors
// planrun.ScanMarkers exactly: whole-stdout-event text scan, adapter-agnostic,
// accepting the same tiny false-positive risk in exchange for working uniformly
// across claude/codex/gemini. Only EventStdout is scanned.
//
// If the reviewer emits more than one marker, the MOST SEVERE wins
// (halt > revise > pass) so an ambiguous review never merges. found is false
// when no marker appears at all; the caller (runner) owns the policy for a
// verdict-less review. Findings is the concatenation of all stdout lines —
// the reviewer's report.
func ScanReviewVerdict(events []coreexec.Event) (verdict Verdict, found bool) {
	var stdout []string
	severity := -1
	for _, ev := range events {
		if ev.Type != coreexec.EventStdout {
			continue
		}
		stdout = append(stdout, ev.Data)
		for _, m := range reviewVerdictRe.FindAllStringSubmatch(ev.Data, -1) {
			if r := severityRank[m[1]]; r > severity {
				severity = r
			}
		}
	}
	if severity < 0 {
		return Verdict{}, false
	}
	return Verdict{Class: classBySeverity[severity], Findings: strings.Join(stdout, "\n")}, true
}
