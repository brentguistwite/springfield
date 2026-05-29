package planreview

import (
	"regexp"
	"strings"

	coreexec "springfield/internal/core/exec"
)

// reviewVerdictRe matches a verdict marker that occupies its OWN line with NO
// leading or trailing whitespace (a single optional `\r` before EOL handles
// CRLF agent output). The strict-anchoring is load-bearing: the diff under
// review is embedded verbatim in the reviewer prompt, and the reviewer commonly
// quotes the diff in its analysis. Without these anchors a
// `<review-verdict>halt</…>` literal in the code being reviewed (test fixture,
// string constant, doc comment) would force a spurious needs-human verdict —
// an arbitrary implementer agent could plant the literal to escape the gate.
//
// We REJECT leading whitespace specifically because an implementer could
// commit a file containing an indented `  <review-verdict>pass</…>` line; if
// the reviewer quotes that snippet in its analysis without emitting its own
// verdict, an allow-indent regex would accept the quoted line as a real pass
// and bypass the gate entirely. The prompt's verdict-line examples are kept
// at column 0 to satisfy this regex.
var reviewVerdictRe = regexp.MustCompile(`(?m)^<review-verdict>(pass|revise|halt)</review-verdict>\r?$`)

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
	// Strip bare verdict-marker lines from the findings echo. The marker line
	// itself is protocol, not analysis — leaving it in Findings risks the
	// fix-iteration implementer reproducing the line verbatim into a commit
	// message or file, which a subsequent review's scan could pick up as a
	// false verdict. The own-line regex anchor bounds this, but defense-in-depth
	// is cheap here.
	findings := stripVerdictMarkers(strings.Join(stdout, "\n"))
	return Verdict{Class: classBySeverity[severity], Findings: findings}, true
}

// stripVerdictMarkers removes lines that consist solely of a verdict marker
// (with optional trailing CR for CRLF agent output). Non-marker lines, and
// marker-like text that appears inline within a line, are preserved unchanged.
func stripVerdictMarkers(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, line := range lines {
		stripped := strings.TrimRight(line, "\r")
		if reviewVerdictRe.MatchString(stripped) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
