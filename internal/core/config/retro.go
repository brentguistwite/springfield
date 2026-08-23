package config

import "strings"

// RetroConfig is the [retro] block from springfield.toml. Retrospective
// extraction is project-wide behavior (a shareable operational policy, not a
// personal secret), so it lives in the committed config beside [verify] and
// [stall], not the git-ignored local file.
//
// The zero value leaves the loop on (Enabled defaults true) with filing
// disabled (empty ItemsDir), so a project with no [retro] block still writes
// retro.json at finalize time but never touches a vault.
type RetroConfig struct {
	// Enabled turns the whole retrospective loop on or off. Pointer so an
	// omitted key (nil → default true) is distinguishable from an explicit
	// enabled = false. Resolve via EnabledOrDefault(); an explicit false must
	// skip retro entirely at the completion call site.
	Enabled *bool `toml:"enabled,omitempty"`
	// ItemsDir is the absolute vault items directory the filer writes recurring
	// patterns into (e.g. "/Users/me/Documents/Obsidian/vault/personal/projects/springfield/items").
	// Empty (the default) disables filing: the loop still extracts and persists
	// retro.json, it just never writes a vault item. A non-empty value must be
	// an absolute path — the filer runs unattended at finalize time and a
	// relative path would resolve against an unpredictable process cwd. Resolve
	// filing intent via FilingEnabled().
	ItemsDir string `toml:"items_dir,omitempty"`
}

// EnabledOrDefault resolves whether the retrospective loop runs. Default true:
// only an explicit enabled = false in springfield.toml turns it off.
func (r RetroConfig) EnabledOrDefault() bool {
	if r.Enabled == nil {
		return true
	}
	return *r.Enabled
}

// FilingEnabled reports whether the filer should write vault items, i.e. an
// items_dir is configured. Mirrors the retro filer's own empty-dir-disables
// contract so the call site can gate filing without duplicating the rule.
func (r RetroConfig) FilingEnabled() bool {
	return strings.TrimSpace(r.ItemsDir) != ""
}
