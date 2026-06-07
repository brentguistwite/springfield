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
	// EnforceProtectedBase refuses preflight when the resolved base ref is in
	// [ProtectedBases]. Off by default so library-level callers stay
	// minimal; cmd/start enables it unless the project opts out via
	// allow_protected_base = true.
	EnforceProtectedBase bool
	// MinFreeDiskBytes is the free-space floor the disk preflight enforces
	// before a fresh worktree checkout. Zero selects [defaultMinFreeDiskBytes].
	// Ignored on a worktree-reuse resume, which neither re-checks-out nor
	// re-installs.
	MinFreeDiskBytes uint64
}

// ProtectedBases is the list of branch names Springfield refuses to ff-merge
// into when EnforceProtectedBase is set. Hardcoded conservatively: most
// teams ship from a feature branch, and an accidental local advance of
// main/master past origin is a sharp foot-gun.
var ProtectedBases = []string{"main", "master"}

// IsProtectedBase reports whether ref names a hardcoded protected branch.
// Exported so the merge-only re-entry path in cmd/start can apply the same
// guard before invoking planmerge.Integrate on a previously-completed plan
// whose state already records BaseRef.
func IsProtectedBase(ref string) bool {
	for _, p := range ProtectedBases {
		if ref == p {
			return true
		}
	}
	return false
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
	// Disk backs the disk-space preflight. A nil Disk skips the check, so
	// existing callers that construct a Manager with only Git keep working.
	Disk DiskChecker
}

// NewManager builds a manager backed by the system git CLI and statfs.
func NewManager() *Manager { return &Manager{Git: CLIGit{}, Disk: cliDisk{}} }

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

	planAbs := filepath.Join(in.ControlRoot, filepath.FromSlash(in.Unit.Path))
	if info, statErr := os.Stat(planAbs); statErr != nil {
		if os.IsNotExist(statErr) {
			return PrepareDecision{}, reject("preflight-plan-missing",
				fmt.Sprintf("plan %q file not found at %s; fix plan_units[].path before running", in.Unit.ID, planAbs))
		}
		return PrepareDecision{}, fmt.Errorf("stat plan file %s: %w", planAbs, statErr)
	} else if info.IsDir() {
		return PrepareDecision{}, reject("preflight-plan-missing",
			fmt.Sprintf("plan %q path %s is a directory, not a file", in.Unit.ID, planAbs))
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
	} else {
		// Slice-3 contract: explicit Ref must name a local branch so the
		// merge phase can publish via `git update-ref refs/heads/<ref>`.
		// Verify before any worktree side effects so an unknown branch is
		// rejected up front instead of failing mid-merge.
		exists, berr := m.Git.BranchExists(in.ControlRoot, baseRef)
		if berr != nil {
			return PrepareDecision{}, fmt.Errorf("check local branch %q: %w", baseRef, berr)
		}
		if !exists {
			return PrepareDecision{}, reject("preflight-ref-not-local-branch",
				fmt.Sprintf("plan %q ref %q is not a local branch in %s; merge integration requires a local branch target", in.Unit.ID, baseRef, in.ControlRoot))
		}
	}

	// effectiveBaseRef is the base the merge phase will actually publish into.
	// On a resume that reuses a worktree, the recorded PriorState.BaseRef
	// wins (matches the firstNonEmpty(...) used to populate Context.BaseRef
	// further down). Computing the same expression here keeps the guard
	// honest about resumes: a plan anchored to a feature branch is not
	// refused just because the operator's current checkout happens to be
	// main/master.
	effectiveBaseRef := baseRef
	if in.PriorState != nil && in.PriorState.BaseRef != "" {
		effectiveBaseRef = in.PriorState.BaseRef
	}
	if in.EnforceProtectedBase && IsProtectedBase(effectiveBaseRef) {
		return PrepareDecision{}, reject("preflight-protected-base",
			fmt.Sprintf("plan %q would ff-merge into protected branch %q. Springfield refuses so the local %s is not silently advanced past origin. Recommended: switch to a feature branch (`git switch -c feat/<name>`) before running. Batch users can also leave [project] auto_branch at its default so the next start auto-cuts a feature branch, or set [project] allow_protected_base = true in springfield.toml to opt out of the guard.",
				in.Unit.ID, effectiveBaseRef, effectiveBaseRef))
	}

	existing := worktreePathsByOwner(in.AllStates, in.Unit.ID)
	wtPath, err := WorktreePath(in.ControlRoot, in.WorktreeBase, in.Unit, existing)
	if err != nil {
		return PrepareDecision{}, err
	}

	// Resume path: prior attempt exists for this plan with a recorded
	// worktree path that still resolves on disk AND is still owned by git
	// as a registered worktree on the recorded branch. A bare directory at
	// the recorded path — even with the right name — is not honest reuse:
	// the path could have been deleted and recreated, or could now belong
	// to an unrelated checkout. Both cases force a fresh attempt.
	if in.PriorState != nil && in.PriorState.WorktreePath != "" {
		recordedPath := in.PriorState.WorktreePath
		recordedBranch := firstNonEmpty(in.PriorState.Branch, branch)
		info, statErr := os.Stat(recordedPath)
		switch {
		case statErr == nil && info.IsDir():
			if in.PriorState.InputDigest != "" && in.PriorState.InputDigest != digest {
				return PrepareDecision{}, reject("preflight-input-drift",
					fmt.Sprintf("plan %q inputs changed since last attempt; reuse refused. Remove %s or reset the plan to re-run with new inputs.", in.Unit.ID, recordedPath))
			}
			registered, lerr := m.Git.WorktreeListPaths(in.ControlRoot)
			if lerr != nil {
				return PrepareDecision{}, fmt.Errorf("list worktrees: %w", lerr)
			}
			if !pathRegistered(registered, recordedPath) {
				return PrepareDecision{}, reject("preflight-worktree-untracked",
					fmt.Sprintf("recorded worktree %s is not a registered git worktree (deleted or recreated outside Springfield); remove the path or reset plan %q to start fresh", recordedPath, in.Unit.ID))
			}
			actualBranch, brErr := m.Git.CurrentBranch(recordedPath)
			if brErr != nil {
				return PrepareDecision{}, reject("preflight-worktree-branch-unreadable",
					fmt.Sprintf("cannot read branch of worktree %s: %v", recordedPath, brErr))
			}
			if actualBranch != recordedBranch {
				return PrepareDecision{}, reject("preflight-worktree-branch-mismatch",
					fmt.Sprintf("worktree %s is on branch %q but plan %q recorded branch %q; refuse reuse", recordedPath, actualBranch, in.Unit.ID, recordedBranch))
			}
			ctx := Context{
				Unit:         in.Unit,
				ControlRoot:  in.ControlRoot,
				WorktreeRoot: recordedPath,
				PlanKey:      PlanKey(in.Unit),
				Branch:       recordedBranch,
				BaseRef:      firstNonEmpty(in.PriorState.BaseRef, baseRef),
				BaseHead:     in.PriorState.BaseHead,
			}
			return PrepareDecision{Context: ctx, InputDigest: digest, Reason: "resume-same-inputs", Reuse: true}, nil
		case statErr == nil && !info.IsDir():
			return PrepareDecision{}, reject("preflight-worktree-collision",
				fmt.Sprintf("recorded worktree path %s is no longer a directory; refuse reuse for plan %q", recordedPath, in.Unit.ID))
		default:
			// Recorded worktree path is missing on disk — treat as first run
			// but keep the recorded path (idempotency: same path next time).
			wtPath = recordedPath
		}
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
		if !pathRegistered(registered, wtPath) {
			return PrepareDecision{}, reject("preflight-worktree-untracked",
				fmt.Sprintf("worktree path %s exists but is not a registered git worktree; refuse reuse for plan %q", wtPath, in.Unit.ID))
		}
		// Registered but not in our state — the plan key has no prior
		// state but git already owns the path. Refuse rather than silently
		// adopting a stranger's checkout.
		return PrepareDecision{}, reject("preflight-worktree-untracked-by-springfield",
			fmt.Sprintf("worktree at %s is registered with git but not tracked in Springfield state; remove or rename it before running plan %q", wtPath, in.Unit.ID))
	}

	// Fresh checkout: refuse up front if free space is below the floor, so a
	// near-full disk fails fast here instead of crashing mid-run with ENOSPC
	// once the agent's install (e.g. node_modules) fills the volume.
	if err := m.checkDisk(in.ControlRoot, in.MinFreeDiskBytes, in.Unit.ID); err != nil {
		return PrepareDecision{}, err
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

// checkDisk enforces the free-space floor before a fresh worktree checkout.
// A nil Disk or an unmeasurable platform skips the check (fail-open): the
// preflight is a guard against the common near-full-disk crash, not a hard
// gate that should block runs it cannot evaluate.
func (m *Manager) checkDisk(root string, minFree uint64, planID string) error {
	if m.Disk == nil {
		return nil
	}
	if minFree == 0 {
		minFree = defaultMinFreeDiskBytes
	}
	avail, err := m.Disk.AvailableBytes(root)
	if err != nil {
		return nil
	}
	if avail < minFree {
		return reject("preflight-insufficient-disk",
			fmt.Sprintf("plan %q: only %s free at %s but a worktree checkout needs at least %s. Each plan worktree is a full checkout plus the agent's install (e.g. node_modules); free space before running.",
				planID, humanBytes(avail), root, humanBytes(minFree)))
	}
	return nil
}

// humanBytes formats a byte count as a compact binary-unit string for
// operator-facing preflight messages.
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
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

func pathRegistered(registered []string, target string) bool {
	for _, p := range registered {
		if equalPaths(p, target) {
			return true
		}
	}
	return false
}

func equalPaths(a, b string) bool {
	return canonicalPath(a) == canonicalPath(b)
}

func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
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
