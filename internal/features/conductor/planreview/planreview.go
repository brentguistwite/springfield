// Package planreview runs an independent reviewer agent against a completed
// plan's diff and parses a structured verdict. It is intentionally git-free and
// batch-free: callers pass the diff string in, and an AgentRunner is injected,
// so the whole package is unit-testable in isolation.
package planreview

// VerdictClass is the reviewer's three-way decision.
type VerdictClass string

const (
	// VerdictPass — work satisfies the criteria; safe to merge.
	VerdictPass VerdictClass = "pass"
	// VerdictRevise — fixable problems; loop the implementer with the findings.
	VerdictRevise VerdictClass = "revise"
	// VerdictHalt — needs a human; not safely fixable by an agent.
	VerdictHalt VerdictClass = "halt"
)

// Verdict is the parsed result of one review. Findings is the reviewer's full
// prose report (all stdout, marker lines stripped), fed verbatim to the
// implementer on a revise. Excerpt is the marker-adjacent slice of that report
// — the block immediately preceding the winning verdict marker — and is the
// operator-facing snippet on a halt/needs-human handoff, where the head of the
// full report is often just opening narration rather than the actual findings.
type Verdict struct {
	Class    VerdictClass
	Findings string
	Excerpt  string
}
