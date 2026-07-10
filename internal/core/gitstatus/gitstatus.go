// Package gitstatus centralizes Springfield's notion of a "clean enough"
// working tree.
//
// Several preflight gates (planrun's slice preflight, planmerge's resync
// gate, and the autobranch activation check) must decide whether the source
// repo is dirty before mutating it. Springfield writes its own bookkeeping
// into the tree — control-plane state under .springfield/, worktree bases
// under .worktrees/, and generated config (springfield.toml plus its .local
// and timestamped .bak- siblings) — and none of that should trip a
// dirty-source refusal even when the operator has not added it to a
// .gitignore or .git/info/exclude.
//
// [Dirty] is the single source of truth for that decision so the gates stay
// in lock-step; a path added here is ignored everywhere at once.
package gitstatus

import "strings"

// ownedDirPrefixes are directory prefixes whose contents are Springfield's
// own bookkeeping rather than user-visible source.
var ownedDirPrefixes = []string{".springfield/", ".worktrees/"}

// ownedPathPrefixes match a file whose name begins with the prefix — used for
// the timestamped springfield.toml.bak-<ts> backups init writes.
var ownedPathPrefixes = []string{"springfield.toml.bak-"}

// ownedExactPaths are whole-file paths Springfield generates and may modify
// in place.
var ownedExactPaths = []string{"springfield.toml", "springfield.local.toml"}

// Dirty reports whether the output of `git status --porcelain` describes any
// change outside the paths Springfield owns. Empty output (a clean tree) is
// never dirty; a tree containing only Springfield-owned changes is likewise
// reported clean so the source preflights do not refuse on Springfield's own
// artifacts.
func Dirty(porcelain string) bool {
	if strings.TrimSpace(porcelain) == "" {
		return false
	}
	for _, line := range strings.Split(porcelain, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !lineIsOwned(line) {
			return true
		}
	}
	return false
}

// lineIsOwned returns true when a `git status --porcelain` line describes a
// Springfield-owned path. Porcelain format is "XY <path>" (or
// "XY <orig> -> <new>" for renames); we strip the two-column status code and
// any rename arrow, then unquote, before matching.
func lineIsOwned(line string) bool {
	if len(line) < 4 {
		return false
	}
	rest := line[3:]
	if idx := strings.Index(rest, " -> "); idx >= 0 {
		rest = rest[idx+len(" -> "):]
	}
	rest = strings.TrimPrefix(rest, "\"")
	rest = strings.TrimSuffix(rest, "\"")

	for _, p := range ownedDirPrefixes {
		if strings.HasPrefix(rest, p) {
			return true
		}
	}
	for _, p := range ownedPathPrefixes {
		if strings.HasPrefix(rest, p) {
			return true
		}
	}
	for _, exact := range ownedExactPaths {
		if rest == exact {
			return true
		}
	}
	return false
}
