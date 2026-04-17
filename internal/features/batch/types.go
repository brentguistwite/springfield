package batch

import "time"

// IntegrationMode controls how slice branches are merged on completion.
type IntegrationMode string

const (
	IntegrationBatch      IntegrationMode = "batch"
	IntegrationStandalone IntegrationMode = "standalone"
	IntegrationMain       IntegrationMode = "main"
)

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
)

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
	Branch  string      `json:"branch,omitempty"`
	Worktree string     `json:"worktree,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// Phase groups slices that share an execution mode.
type Phase struct {
	Mode   PhaseMode `json:"mode"`
	Slices []string  `json:"slices"` // ordered slice IDs
}

// Batch is the compile-time state for one Springfield work batch.
type Batch struct {
	ID              string          `json:"id"`
	Title           string          `json:"title"`
	SourceKind      SourceKind      `json:"source_kind"`
	IntegrationMode IntegrationMode `json:"integration_mode"`
	Phases          []Phase         `json:"phases"`
	Slices          []Slice         `json:"slices"`
}

// Run is the runtime-only cursor state for the active batch.
type Run struct {
	ActiveBatchID   string    `json:"active_batch_id"`
	ActivePhaseIdx  int       `json:"active_phase_idx"`
	ActiveSliceIDs  []string  `json:"active_slice_ids,omitempty"`
	LastCheckpoint  time.Time `json:"last_checkpoint,omitempty"`
	LastBranch      string    `json:"last_branch,omitempty"`
	LastWorktree    string    `json:"last_worktree,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
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
