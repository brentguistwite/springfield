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

// Verdict is the parsed result of one review. Findings is the reviewer's prose
// report (stdout), fed verbatim to the implementer on a revise and surfaced to
// the operator on a halt.
type Verdict struct {
	Class    VerdictClass
	Findings string
}
