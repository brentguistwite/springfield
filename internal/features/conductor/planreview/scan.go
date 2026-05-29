package planreview

import (
	"regexp"
	"strings"

	coreexec "springfield/internal/core/exec"
)

// reviewVerdictRe matches a verdict marker that occupies its OWN line. The
// `(?m)^...\s*$` anchoring is load-bearing: the diff under review is embedded
// verbatim in the reviewer prompt, and the reviewer commonly quotes the diff
// in its analysis. Without the line anchors a `<review-verdict>halt</…>`
// literal in the code being reviewed (test fixture, string constant, doc
// comment) would force a spurious needs-human verdict — an arbitrary
// implementer agent could plant the literal to escape the gate. The prompt
// asks reviewers to emit the marker on its own line; this anchor enforces it.
var reviewVerdictRe = regexp.MustCompile(`(?m)^[ \t]*<review-verdict>(pass|revise|halt)</review-verdict>[ \t]*$`)

var severityRank = map[string]int{"pass": 0, "revise": 1, "halt": 2}
var classBySeverity = []VerdictClass{VerdictPass, VerdictRevise, VerdictHalt}

// ScanReviewVerdict extracts the reviewer's verdict from agent stdout. Only
// markers that occupy their own line are recognized (see reviewVerdictRe) so a
// quoted diff fragment cannot impersonate a verdict.
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
