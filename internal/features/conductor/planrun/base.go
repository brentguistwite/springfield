package planrun

import (
	"fmt"
	"strings"
)

// ResolveBatchBase resolves the batch-wide base ref from the explicit --base
// flag and the configured [project] base_branch, falling back to the current
// branch. Precedence: flagBase > configBase > current branch.
//
// A detached HEAD (or otherwise unreadable current branch) with no flag/config
// base is rejected with a structured preflight error rather than silently
// picking a SHA — an unattended controller must pass --base or set base_branch
// so the current-branch fallback stays manual-only.
//
// Per-plan Unit.Ref precedence sits ABOVE this and is applied in Prepare (a
// plan with its own Ref ignores the batch base). This resolver computes only
// the fallback every Ref-less plan inherits, which Prepare receives via
// PrepareInput.BatchBaseRef.
func ResolveBatchBase(g Git, controlRoot, flagBase, configBase string) (string, error) {
	if b := strings.TrimSpace(flagBase); b != "" {
		return b, nil
	}
	if b := strings.TrimSpace(configBase); b != "" {
		return b, nil
	}
	cur, err := g.CurrentBranch(controlRoot)
	if err != nil {
		return "", reject("preflight-detached-head",
			fmt.Sprintf("cannot resolve a base branch in %s: %v; pass --base <branch> or set [project] base_branch in springfield.toml", controlRoot, err))
	}
	return cur, nil
}
