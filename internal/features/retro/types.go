package retro

import "time"

// Report is the structured retrospective for one finished batch.
//
// It is keyed by batch ID, never by plan ID: the conductor reuses plan IDs
// across batches (iteration counters restart at 1), so only the batch ID
// uniquely identifies the run a report describes. Two reports for plan "US-001"
// are two different batches; conflating them would fold unrelated evidence.
type Report struct {
	BatchID    string    `json:"batch_id"`
	Title      string    `json:"title,omitempty"`
	ArchivedAt time.Time `json:"archived_at,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	// BatchMode is the branch-output mode the batch ran in ("consolidate" |
	// "per-plan"). It changes how a plan's absence of a Branch should be read
	// (consolidate deletes branches; per-plan keeps them), so classifiers need
	// it to avoid a false merge-refused on a normally-merged consolidate plan.
	BatchMode string      `json:"batch_mode,omitempty"`
	TotalUSD  float64     `json:"total_usd,omitempty"`
	Plans     []PlanRetro `json:"plans,omitempty"`
	// Findings is populated by the classifiers (see US-002); Extract leaves it
	// empty. A degraded extraction still produces a valid, findings-less report.
	Findings []Finding `json:"findings,omitempty"`
	// Degraded records non-fatal extraction problems (a missing or corrupt
	// archive.json, evidence that could not be located or parsed) as stable,
	// human-readable notes. Extract never fails the whole run for a bad file —
	// it records the gap here and moves on — so a consumer can still tell a
	// clean report from one assembled over holes.
	Degraded []string `json:"degraded,omitempty"`
}

// PlanRetro is the per-plan slice of a [Report]: the archive record (identity,
// branch trail, final status) joined with the plan's relocated evidence tail
// (summary.json, iter-N/, stalls.jsonl, verify-iter-*).
type PlanRetro struct {
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	Status  string `json:"status,omitempty"`   // archive record status (freeform forensic string)
	Branch  string `json:"branch,omitempty"`   // springfield/<plan> branch; empty in consolidate mode
	BaseRef string `json:"base_ref,omitempty"` // base the plan branched from

	// The following three mirror summary.json, written once at loop exit.
	IterationCount int    `json:"iteration_count,omitempty"`
	TerminalStatus string `json:"terminal_status,omitempty"`
	ExitReason     string `json:"exit_reason,omitempty"`

	Iterations   []IterationRetro `json:"iterations,omitempty"`
	Stalls       int              `json:"stalls,omitempty"`        // count of stalls.jsonl wedge records
	VerifyRounds []VerifyRetro    `json:"verify_rounds,omitempty"` // one per verify-iter-<round> dir

	// EvidenceSource records where the evidence tail was read from: "archive"
	// (the relocated copy under batchDir/plans/<key>), "execution" (the fallback
	// at .springfield/execution/plans/<key> when relocation was skipped), or ""
	// when no evidence was found at all. EvidenceMissing is the "" case surfaced
	// as a boolean so callers need not compare strings.
	EvidenceSource  string `json:"evidence_source,omitempty"`
	EvidenceMissing bool   `json:"evidence_missing,omitempty"`
}

// IterationRetro is one iter-N directory: the winning agent's meta.json joined
// with its cost.json, plus the ordered agent chain when the iteration fell
// through multiple agents (claude → codex → …). A chain longer than one attempt
// is the raw signal a fallback-storm classifier keys on.
type IterationRetro struct {
	Index          int      `json:"index"`
	AgentID        string   `json:"agent_id,omitempty"`
	Model          string   `json:"model,omitempty"`
	ExitCode       int      `json:"exit_code"`
	Classification string   `json:"classification,omitempty"`
	Error          string   `json:"error,omitempty"`
	Adapter        string   `json:"adapter,omitempty"`
	CostUSD        float64  `json:"cost_usd,omitempty"`
	Attempts       []string `json:"attempts,omitempty"` // agent ids of iter-N/<agent>/ subdirs, in dispatch order
}

// VerifyRetro is one verify-iter-<round> directory: the round's verify.json
// exit code and timeout flag. A run of non-zero rounds is what a
// verify-nonconvergence classifier reads.
type VerifyRetro struct {
	Round    int  `json:"round"`
	ExitCode int  `json:"exit_code"`
	TimedOut bool `json:"timed_out,omitempty"`
}

// Finding is one classifier verdict over a report. PatternKey is the stable
// machine key (e.g. "iteration-cap"); PlanIDs and EvidenceRefs point at the
// evidence that triggered it. Extract does not emit findings — the classifiers
// (US-002) do — but the type lives here so [Report] is complete.
type Finding struct {
	PatternKey   string   `json:"pattern_key"`
	Severity     string   `json:"severity"`
	PlanIDs      []string `json:"plan_ids,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	Summary      string   `json:"summary"`
}
