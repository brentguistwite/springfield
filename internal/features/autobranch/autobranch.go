// Package autobranch auto-cuts a feature branch when `springfield start`
// runs on a protected base ("main"/"master") with auto-branching enabled.
//
// The package has two public surfaces:
//
//   - [ResolveBranchName] is a pure function that renders the configured
//     pattern, validates placeholders, and resolves collisions by appending
//     a numeric suffix.
//   - [Activate] / [Restore] orchestrate the branch creation and the
//     post-batch switch-back. The caller injects a Git implementation so
//     tests do not shell out.
//
// Auto-branching only fires on a fresh run: when [Input].PriorBranch matches
// the rendered name (i.e. the operator is already on a Springfield-cut
// auto-branch from a prior interrupted run), [Activate] is a no-op so a
// resumed batch keeps writing to the same branch.
package autobranch

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Git is the minimal git surface autobranch needs. Mirrors the planrun.Git
// shape so the same CLIGit implementation can satisfy both.
type Git interface {
	CurrentBranch(dir string) (string, error)
	BranchExists(dir, branch string) (bool, error)
	IsDirty(dir string) (bool, error)
	SwitchCreate(dir, branch string) error
	Switch(dir, branch string) error
}

// MaxCollisionAttempts caps how many numeric suffixes [ResolveBranchName]
// will try before giving up. Ten is more than any real workflow needs and
// keeps a runaway suffix loop bounded.
const MaxCollisionAttempts = 10

var placeholderRE = regexp.MustCompile(`\{[^}]*\}`)

// ResolveBranchName renders pattern with the supplied batchID and resolves
// collisions by appending "-2", "-3", ... up to [MaxCollisionAttempts].
// branchExists may be nil for callers that don't care about collisions
// (tests).
//
// Errors:
//   - empty pattern
//   - unknown placeholder (only "{id}" is supported)
//   - collision exhausts the attempt cap
func ResolveBranchName(pattern, batchID string, branchExists func(string) (bool, error)) (string, error) {
	if strings.TrimSpace(pattern) == "" {
		return "", fmt.Errorf("auto_branch_pattern must not be empty")
	}
	if strings.TrimSpace(batchID) == "" {
		return "", fmt.Errorf("batch ID must not be empty")
	}

	for _, ph := range placeholderRE.FindAllString(pattern, -1) {
		if ph != "{id}" {
			return "", fmt.Errorf("auto_branch_pattern uses unsupported placeholder %q (supported: {id})", ph)
		}
	}

	base := strings.ReplaceAll(pattern, "{id}", batchID)

	if branchExists == nil {
		return base, nil
	}

	exists, err := branchExists(base)
	if err != nil {
		return "", fmt.Errorf("check branch %q: %w", base, err)
	}
	if !exists {
		return base, nil
	}

	for i := 2; i <= MaxCollisionAttempts; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		exists, err := branchExists(candidate)
		if err != nil {
			return "", fmt.Errorf("check branch %q: %w", candidate, err)
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("auto-branch name %q and suffixes through %d already exist; clean up old auto-branches or set auto_branch_pattern", base, MaxCollisionAttempts)
}

// Input collects the data Activate needs.
type Input struct {
	Git Git
	// Dir is the repository root.
	Dir string
	// BatchID is the active batch identifier (used for {id}).
	BatchID string
	// Pattern is the configured auto_branch_pattern. Caller is expected to
	// pass the resolved value (config.Config.AutoBranchPatternOrDefault()).
	Pattern string
	// Enabled mirrors config.Config.AutoBranchEnabled(). When false,
	// Activate is an explicit no-op (returns nil Activation, nil error).
	Enabled bool
	// AlreadyAutoBranch hints that a prior run already auto-cut and the
	// operator is currently on it. When true, Activate skips creation and
	// returns an Activation describing the in-progress state so Restore can
	// still switch back to the recorded original branch.
	AlreadyAutoBranch bool
	// PriorOriginalBranch is the original-branch value persisted by a prior
	// interrupted run. Required when AlreadyAutoBranch is true.
	PriorOriginalBranch string
	// PriorAutoBranchName is the auto-branch name persisted by a prior
	// interrupted run. Required when AlreadyAutoBranch is true.
	PriorAutoBranchName string
	// BeforePersistCreate is called on the create path after the auto-branch
	// name is resolved and the dirty/protected-base checks pass, but BEFORE
	// the git switch creates the branch. The caller uses this hook to
	// durably record the intended OriginalBranch and BranchName so a crash
	// mid-switch leaves recoverable state instead of an orphan branch the
	// next run can't tie back to a recorded original. Returning an error
	// aborts Activate before any git side effect.
	//
	// Not called on the resume path or when auto-branching is disabled.
	BeforePersistCreate func(originalBranch, branchName string) error
}

// Activation describes the state the runner needs to roll back at the end
// of the batch. A nil *Activation from [Activate] means "nothing to undo".
type Activation struct {
	// OriginalBranch is the branch the operator was on before Activate ran.
	OriginalBranch string
	// BranchName is the auto-cut feature branch the batch will run on.
	BranchName string
	// Reason is a short tag for logs ("created", "resumed").
	Reason string
}

// IsProtectedBase reports whether ref names a hardcoded protected branch.
// Mirrored from planrun.IsProtectedBase to avoid a cross-package import
// for callers that only need the auto-branch decision.
func IsProtectedBase(ref string) bool {
	return ref == "main" || ref == "master"
}

// Activate evaluates whether to auto-cut a branch and performs the switch.
//
// Returns (nil, nil) when:
//   - in.Enabled is false, or
//   - the current branch is not in the protected list.
//
// Returns an error when the working tree is dirty (the operator must
// commit or stash first), when ResolveBranchName fails, or when git switch
// fails.
func Activate(in Input, out io.Writer) (*Activation, error) {
	if in.Git == nil {
		return nil, fmt.Errorf("autobranch.Activate: Git is required")
	}
	if in.Dir == "" {
		return nil, fmt.Errorf("autobranch.Activate: Dir is required")
	}

	if in.AlreadyAutoBranch {
		if in.PriorOriginalBranch == "" || in.PriorAutoBranchName == "" {
			return nil, fmt.Errorf("autobranch.Activate: AlreadyAutoBranch requires PriorOriginalBranch and PriorAutoBranchName")
		}
		current, err := in.Git.CurrentBranch(in.Dir)
		if err != nil {
			return nil, fmt.Errorf("resolve current branch: %w", err)
		}
		if current != in.PriorAutoBranchName {
			dirty, derr := in.Git.IsDirty(in.Dir)
			if derr != nil {
				return nil, fmt.Errorf("check working tree before resume switch: %w", derr)
			}
			if dirty {
				return nil, fmt.Errorf("working tree on %q has uncommitted changes; commit or stash before resuming so the switch back to %q is safe", current, in.PriorAutoBranchName)
			}
			if err := in.Git.Switch(in.Dir, in.PriorAutoBranchName); err != nil {
				return nil, fmt.Errorf("switch to auto-branch %s for resume: %w", in.PriorAutoBranchName, err)
			}
		}
		fmt.Fprintf(out, "auto-branch resume: continuing on %s (will switch back to %s on finish)\n",
			in.PriorAutoBranchName, in.PriorOriginalBranch)
		return &Activation{
			OriginalBranch: in.PriorOriginalBranch,
			BranchName:     in.PriorAutoBranchName,
			Reason:         "resumed",
		}, nil
	}

	if !in.Enabled {
		return nil, nil
	}

	current, err := in.Git.CurrentBranch(in.Dir)
	if err != nil {
		return nil, fmt.Errorf("resolve current branch: %w", err)
	}
	if !IsProtectedBase(current) {
		return nil, nil
	}

	dirty, err := in.Git.IsDirty(in.Dir)
	if err != nil {
		return nil, fmt.Errorf("check working tree: %w", err)
	}
	if dirty {
		return nil, fmt.Errorf("working tree on %q has uncommitted changes; commit or stash before running springfield start so the auto-branch starts from a clean snapshot", current)
	}

	name, err := ResolveBranchName(in.Pattern, in.BatchID, func(b string) (bool, error) {
		return in.Git.BranchExists(in.Dir, b)
	})
	if err != nil {
		return nil, err
	}

	if in.BeforePersistCreate != nil {
		if perr := in.BeforePersistCreate(current, name); perr != nil {
			return nil, fmt.Errorf("persist auto-branch state pre-switch: %w", perr)
		}
	}

	if err := in.Git.SwitchCreate(in.Dir, name); err != nil {
		return nil, fmt.Errorf("git switch -c %s: %w", name, err)
	}

	fmt.Fprintf(out, "auto-cut branch %s from %s\n", name, current)
	fmt.Fprintf(out, "  → all slice work will merge here\n")
	fmt.Fprintf(out, "  → switching back to %s on finish (push + PR by hand)\n", current)

	return &Activation{
		OriginalBranch: current,
		BranchName:     name,
		Reason:         "created",
	}, nil
}

// Outcome tells [Restore] which close-out message to print.
type Outcome int

const (
	// OutcomeSuccess: batch completed cleanly. Restore prints push/PR
	// instructions because the auto-branch holds work the operator needs to
	// publish.
	OutcomeSuccess Outcome = iota
	// OutcomeFailed: batch surfaced an error. Restore prints inspection
	// instructions because the auto-branch may hold partial work.
	OutcomeFailed
	// OutcomeInterrupted: batch was cancelled by signal. Restore prints
	// resume instructions because the next `springfield start` will pick
	// the auto-branch back up.
	OutcomeInterrupted
)

// Restore switches back to the original branch and prints the close-out
// message keyed on outcome.
//
// When the switch itself fails, the operator is left on the auto-branch and
// Restore returns the switch error so the caller can surface it.
func Restore(g Git, dir string, a *Activation, outcome Outcome, out io.Writer) error {
	if a == nil {
		return nil
	}
	if g == nil {
		return fmt.Errorf("autobranch.Restore: Git is required")
	}
	if err := g.Switch(dir, a.OriginalBranch); err != nil {
		fmt.Fprintf(out, "auto-branch: failed to switch back to %s; you are still on %s\n", a.OriginalBranch, a.BranchName)
		fmt.Fprintf(out, "  remediation: resolve any uncommitted changes, then run: git switch %s\n", a.OriginalBranch)
		return fmt.Errorf("switch back to %s: %w", a.OriginalBranch, err)
	}
	switch outcome {
	case OutcomeSuccess:
		fmt.Fprintf(out, "batch complete on %s\n", a.BranchName)
		fmt.Fprintf(out, "switched back to %s\n", a.OriginalBranch)
		fmt.Fprintf(out, "push + open PR:\n")
		fmt.Fprintf(out, "  git push -u origin %s\n", a.BranchName)
		fmt.Fprintf(out, "  gh pr create\n")
	case OutcomeFailed:
		fmt.Fprintf(out, "batch failed on %s\n", a.BranchName)
		fmt.Fprintf(out, "switched back to %s; auto-branch preserved for inspection:\n", a.OriginalBranch)
		fmt.Fprintf(out, "  git switch %s\n", a.BranchName)
	case OutcomeInterrupted:
		fmt.Fprintf(out, "batch interrupted on %s\n", a.BranchName)
		fmt.Fprintf(out, "switched back to %s; rerun \"springfield start\" to resume on %s\n", a.OriginalBranch, a.BranchName)
	}
	return nil
}
