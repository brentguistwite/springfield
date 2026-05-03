package planrun

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"springfield/internal/features/conductor"
)

// PrepareInput collects the data needed to derive an execution context.
// PriorState is nil when no prior attempt has been recorded for this unit.
type PrepareInput struct {
	ControlRoot  string
	WorktreeBase string
	Unit         conductor.PlanUnit
	PriorState   *conductor.PlanState
	// AllStates lets WorktreePath honor previously-recorded paths for sibling
	// plans so a sanitized-key collision doesn't overwrite a sibling's
	// worktree. Pass project.State.Plans directly.
	AllStates map[string]*conductor.PlanState
}

// PrepareDecision describes what Prepare resolved without yet touching disk.
// Reason is a short tag for logs/state ("clean-first-run", "resume-clean",
// "resume-dirty-owned"). InputDigest is the freshly computed digest.
type PrepareDecision struct {
	Context     Context
	InputDigest string
	Reason      string
	// Reuse is true when an existing worktree should be reused instead of
	// created. Callers must not call WorktreeAdd* when Reuse is true.
	Reuse bool
}

// PreflightError is the structured rejection from the preflight matrix. Tag
// is a short stable identifier suitable for state.exit_reason.
type PreflightError struct {
	Tag     string
	Message string
}

func (e *PreflightError) Error() string { return e.Message }

// reject builds a PreflightError with a matching tag.
func reject(tag, msg string) error { return &PreflightError{Tag: tag, Message: msg} }

// Manager owns the create/reuse decision plus the preflight matrix.
type Manager struct {
	Git Git
}

// NewManager builds a manager backed by the system git CLI.
func NewManager() *Manager { return &Manager{Git: CLIGit{}} }

// Prepare evaluates the preflight matrix and resolves the execution context
// without yet modifying disk. The returned decision tells the caller whether
// to create a new worktree (Reuse == false) or reuse an existing one.
//
// Preflight matrix:
//
//   - Repo must be a git working tree.
//   - First run (no prior worktree path): source must be clean; otherwise
//     reject with tag "preflight-dirty-source".
//   - Resume with same plan owning the worktree path: input digest must
//     match the prior digest; otherwise reject with tag
//     "preflight-input-drift" so a different set of instructions does not
//     silently reuse the old worktree.
//   - Resume with prior state but no recorded worktree path: treated like
//     first run.
//   - Resume after completion: reject with tag "preflight-already-completed"
//     so callers do not silently re-execute a finished plan.
func (m *Manager) Prepare(in PrepareInput) (PrepareDecision, error) {
	if in.ControlRoot == "" {
		return PrepareDecision{}, fmt.Errorf("control root is required")
	}
	repo, err := m.Git.IsRepo(in.ControlRoot)
	if err != nil {
		return PrepareDecision{}, fmt.Errorf("git repo check: %w", err)
	}
	if !repo {
		return PrepareDecision{}, reject("preflight-not-a-repo",
			fmt.Sprintf("Springfield requires a git repo at %s for worktree-based plan execution", in.ControlRoot))
	}

	digest, err := InputDigest(in.ControlRoot, in.Unit)
	if err != nil {
		return PrepareDecision{}, fmt.Errorf("input digest: %w", err)
	}

	if in.PriorState != nil && in.PriorState.Status == conductor.StatusCompleted {
		return PrepareDecision{}, reject("preflight-already-completed",
			fmt.Sprintf("plan %q already completed; remove the plan unit or reset state before rerunning", in.Unit.ID))
	}

	branch := BranchName(in.Unit)
	baseRef := in.Unit.Ref
	if baseRef == "" {
		baseRef, err = m.Git.CurrentBranch(in.ControlRoot)
		if err != nil {
			return PrepareDecision{}, fmt.Errorf("resolve base ref: %w", err)
		}
	}

	existing := worktreePathsByOwner(in.AllStates, in.Unit.ID)
	wtPath, err := WorktreePath(in.ControlRoot, in.WorktreeBase, in.Unit, existing)
	if err != nil {
		return PrepareDecision{}, err
	}

	// Resume path: prior attempt exists for this plan with a recorded
	// worktree path that still resolves on disk.
	if in.PriorState != nil && in.PriorState.WorktreePath != "" {
		recordedPath := in.PriorState.WorktreePath
		if info, err := os.Stat(recordedPath); err == nil && info.IsDir() {
			if in.PriorState.InputDigest != "" && in.PriorState.InputDigest != digest {
				return PrepareDecision{}, reject("preflight-input-drift",
					fmt.Sprintf("plan %q inputs changed since last attempt; reuse refused. Remove %s or reset the plan to re-run with new inputs.", in.Unit.ID, recordedPath))
			}
			ctx := Context{
				Unit:         in.Unit,
				ControlRoot:  in.ControlRoot,
				WorktreeRoot: recordedPath,
				PlanKey:      PlanKey(in.Unit),
				Branch:       firstNonEmpty(in.PriorState.Branch, branch),
				BaseRef:      firstNonEmpty(in.PriorState.BaseRef, baseRef),
				BaseHead:     in.PriorState.BaseHead,
			}
			return PrepareDecision{Context: ctx, InputDigest: digest, Reason: "resume-same-inputs", Reuse: true}, nil
		}
		// Recorded worktree path is missing on disk — treat as first run
		// but keep the recorded path (idempotency: same path next time).
		wtPath = recordedPath
	}

	// First-run path: source must be clean to ensure the worktree we create
	// branches from a coherent base.
	dirty, err := m.Git.IsDirty(in.ControlRoot)
	if err != nil {
		return PrepareDecision{}, fmt.Errorf("source dirty check: %w", err)
	}
	if dirty {
		return PrepareDecision{}, reject("preflight-dirty-source",
			fmt.Sprintf("source checkout %s has uncommitted changes; commit or stash before running plan %q", in.ControlRoot, in.Unit.ID))
	}

	// Worktree path must not be occupied by an unrelated checkout.
	if info, statErr := os.Stat(wtPath); statErr == nil {
		if !info.IsDir() {
			return PrepareDecision{}, reject("preflight-worktree-collision",
				fmt.Sprintf("worktree path %s is not a directory; cannot create worktree for plan %q", wtPath, in.Unit.ID))
		}
		registered, lerr := m.Git.WorktreeListPaths(in.ControlRoot)
		if lerr != nil {
			return PrepareDecision{}, fmt.Errorf("list worktrees: %w", lerr)
		}
		owned := false
		for _, p := range registered {
			if equalPaths(p, wtPath) {
				owned = true
				break
			}
		}
		if !owned {
			return PrepareDecision{}, reject("preflight-worktree-untracked",
				fmt.Sprintf("worktree path %s exists but is not a registered git worktree; refuse reuse for plan %q", wtPath, in.Unit.ID))
		}
		// Registered but not in our state — the plan key has no prior
		// state but git already owns the path. Refuse rather than silently
		// adopting a stranger's checkout.
		return PrepareDecision{}, reject("preflight-worktree-untracked-by-springfield",
			fmt.Sprintf("worktree at %s is registered with git but not tracked in Springfield state; remove or rename it before running plan %q", wtPath, in.Unit.ID))
	}

	// Resolve base head best-effort. If the ref does not resolve yet, fail
	// loudly: a missing base ref is a configuration error.
	baseHead, err := m.Git.ResolveRef(in.ControlRoot, baseRef)
	if err != nil {
		return PrepareDecision{}, fmt.Errorf("resolve base head for %s: %w", baseRef, err)
	}

	ctx := Context{
		Unit:         in.Unit,
		ControlRoot:  in.ControlRoot,
		WorktreeRoot: wtPath,
		PlanKey:      PlanKey(in.Unit),
		Branch:       branch,
		BaseRef:      baseRef,
		BaseHead:     baseHead,
	}
	return PrepareDecision{Context: ctx, InputDigest: digest, Reason: "clean-first-run", Reuse: false}, nil
}

// CreateWorktree materializes the worktree on disk for a fresh attempt.
// Reuse runs must skip this method. CreateWorktree decides between adding a
// new branch (when Branch does not yet exist on disk) and reusing an
// existing branch by name.
func (m *Manager) CreateWorktree(ctx Context) error {
	if ctx.WorktreeRoot == "" {
		return fmt.Errorf("worktree root must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(ctx.WorktreeRoot), 0o755); err != nil {
		return fmt.Errorf("create worktree parent dir: %w", err)
	}
	exists, err := m.Git.BranchExists(ctx.ControlRoot, ctx.Branch)
	if err != nil {
		return err
	}
	if exists {
		return m.Git.WorktreeAddExistingBranch(ctx.ControlRoot, ctx.WorktreeRoot, ctx.Branch)
	}
	return m.Git.WorktreeAddNewBranch(ctx.ControlRoot, ctx.WorktreeRoot, ctx.Branch, ctx.BaseRef)
}

func worktreePathsByOwner(states map[string]*conductor.PlanState, exclude string) map[string]string {
	out := make(map[string]string, len(states))
	for id, st := range states {
		if id == exclude || st == nil || st.WorktreePath == "" {
			continue
		}
		// Use the plan unit ID as the owning key. The PlanKey may equal the
		// ID for slug-shaped IDs; collision protection runs against the set
		// of recorded paths regardless of how they were keyed.
		out[id] = st.WorktreePath
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func equalPaths(a, b string) bool {
	ca, err := filepath.Abs(a)
	if err != nil {
		ca = a
	}
	cb, err := filepath.Abs(b)
	if err != nil {
		cb = b
	}
	return filepath.Clean(ca) == filepath.Clean(cb)
}

// AsPreflight returns err as *PreflightError when it carries a structured tag.
// Returns nil when err is nil or not a preflight rejection.
func AsPreflight(err error) *PreflightError {
	if err == nil {
		return nil
	}
	var pe *PreflightError
	if errors.As(err, &pe) {
		return pe
	}
	return nil
}
