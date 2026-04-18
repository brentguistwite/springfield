package batch

import "time"

// PhaseMode controls whether slices in a phase run serially or in parallel.
type PhaseMode string

const (
	PhaseSerial   PhaseMode = "serial"
	PhaseParallel PhaseMode = "parallel"
)

// SliceStatus is the lifecycle state of one batch slice.
type SliceStatus string

const (
	SliceQueued  SliceStatus = "queued"
	SliceRunning SliceStatus = "running"
	SliceBlocked SliceStatus = "blocked"
	SliceDone    SliceStatus = "done"
	SliceFailed  SliceStatus = "failed"
	SliceAborted SliceStatus = "aborted"
)

// IsTerminal reports whether a slice status represents a settled outcome.
// Non-terminal statuses get rewritten to SliceAborted during archive normalization.
func (s SliceStatus) IsTerminal() bool {
	switch s {
	case SliceDone, SliceFailed, SliceAborted:
		return true
	}
	return false
}

// SourceKind identifies how the batch was compiled.
type SourceKind string

const (
	SourceFile   SourceKind = "file"
	SourcePrompt SourceKind = "prompt"
)

// Slice is one execution unit within a batch phase.
type Slice struct {
	ID      string      `json:"id"`
	Title   string      `json:"title"`
	Summary string      `json:"summary,omitempty"`
	Status  SliceStatus `json:"status"`
	Error   string      `json:"error,omitempty"`
}

// Phase groups slices that share an execution mode.
type Phase struct {
	Mode   PhaseMode `json:"mode"`
	Slices []string  `json:"slices"` // ordered slice IDs
}

// Batch is the compile-time state for one Springfield work batch.
type Batch struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	SourceKind SourceKind `json:"source_kind"`
	Phases     []Phase    `json:"phases"`
	Slices     []Slice    `json:"slices"`
}

// Run is the runtime-only cursor state for the active batch.
type Run struct {
	ActiveBatchID  string    `json:"active_batch_id"`
	ActivePhaseIdx int       `json:"active_phase_idx"`
	ActiveSliceIDs []string  `json:"active_slice_ids,omitempty"`
	LastCheckpoint time.Time `json:"last_checkpoint,omitempty"`
	// FatalError is set only on terminal failure that requires user intervention.
	// Recoverable errors (agent retries, transient slice failures that will resume)
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
	BatchID   string    `json:"batch_id"`
	Title     string    `json:"title"`
	ArchivedAt time.Time `json:"archived_at"`
	Reason    string    `json:"reason,omitempty"`
	Slices    []ArchiveSlice `json:"slices,omitempty"`
}

// ArchiveSlice is the per-slice summary in an archive entry.
type ArchiveSlice struct {
	ID     string      `json:"id"`
	Title  string      `json:"title"`
	Status SliceStatus `json:"status"`
}

// SliceByID returns the slice with the given id, or false if not found.
func (b *Batch) SliceByID(id string) (Slice, bool) {
	for _, s := range b.Slices {
		if s.ID == id {
			return s, true
		}
	}
	return Slice{}, false
}

// UpdateSlice replaces the slice with the matching ID.
func (b *Batch) UpdateSlice(updated Slice) {
	for i, s := range b.Slices {
		if s.ID == updated.ID {
			b.Slices[i] = updated
			return
		}
	}
}

// HasRunningSlice reports whether any slice is currently running.
func (b *Batch) HasRunningSlice() bool {
	for _, s := range b.Slices {
		if s.Status == SliceRunning {
			return true
		}
	}
	return false
}

// ActivePhase returns the current phase, or false when all are done.
func (b *Batch) ActivePhase(phaseIdx int) (Phase, bool) {
	if phaseIdx < 0 || phaseIdx >= len(b.Phases) {
		return Phase{}, false
	}
	return b.Phases[phaseIdx], true
}
