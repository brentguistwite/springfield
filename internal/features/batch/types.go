package batch

import "time"

// PhaseMode controls whether plans in a phase run serially or in parallel.
type PhaseMode string

const (
	PhaseSerial   PhaseMode = "serial"
	PhaseParallel PhaseMode = "parallel"
)

// Phase groups plan IDs that share an execution mode.
type Phase struct {
	Mode  PhaseMode `json:"mode"`
	Plans []string  `json:"plans"` // ordered plan IDs
}

// Batch is the compile-time state for one Springfield work batch.
type Batch struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Phases []Phase `json:"phases"`
	// PlanIDs is the ordered-unique union of all plan IDs referenced across
	// phases[].plans, preserving first-seen order. Derived by Compile; callers
	// must not mutate it. Used as the canonical superset for fast existence checks.
	PlanIDs []string `json:"plan_ids"`
}

// Run is the runtime-only cursor state for the active batch.
type Run struct {
	ActiveBatchID  string    `json:"active_batch_id"`
	ActivePhaseIdx int       `json:"active_phase_idx"`
	ActivePlanIDs  []string  `json:"active_plan_ids,omitempty"`
	LastCheckpoint time.Time `json:"last_checkpoint,omitempty"`
	// FatalError is set only on terminal failure that requires user intervention.
	// Recoverable errors (agent retries, transient plan failures that will resume)
	// are appended to LastRetry instead so FatalError stays a reliable post-mortem signal.
	FatalError string   `json:"fatal_error,omitempty"`
	LastRetry  []string `json:"last_retry,omitempty"`
}

const maxLastRetry = 10

// AppendRetry records a recoverable error onto the retry log (capped).
func (r *Run) AppendRetry(msg string) {
	if msg == "" {
		return
	}
	r.LastRetry = append(r.LastRetry, msg)
	if len(r.LastRetry) > maxLastRetry {
		r.LastRetry = r.LastRetry[len(r.LastRetry)-maxLastRetry:]
	}
}

// ArchiveEntry is the compact summary stored after a batch completes or is replaced.
type ArchiveEntry struct {
	BatchID    string        `json:"batch_id"`
	Title      string        `json:"title"`
	ArchivedAt time.Time     `json:"archived_at"`
	Reason     string        `json:"reason,omitempty"`
	Plans      []ArchivePlan `json:"plans,omitempty"`
}

// ArchivePlan is the per-plan summary in an archive entry.
// Status is a freeform string carried for archive forensics.
type ArchivePlan struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// ActivePhase returns the current phase, or false when all are done.
func (b *Batch) ActivePhase(phaseIdx int) (Phase, bool) {
	if phaseIdx < 0 || phaseIdx >= len(b.Phases) {
		return Phase{}, false
	}
	return b.Phases[phaseIdx], true
}
