package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitDefaultWarnsOnTrackedSpringfieldBlock pins US-005: in the team-safe
// default (info/exclude) mode, if the tracked .gitignore already carries a
// Springfield block — left by an older --tracked-gitignore init or added by
// hand — init warns the operator and leaves the tracked file byte-for-byte
// unchanged. Default mode never edits a .gitignore; the warning surfaces the
// duplicate source of truth instead of silently leaving it.
func TestInitDefaultWarnsOnTrackedSpringfieldBlock(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)

	gitignore := filepath.Join(dir, ".gitignore")
	seeded := "bin/\n\n# Springfield — plans tracked; runtime state local-only\n" +
		".springfield/*\n!.springfield/plans/\n.springfield/plans/*/\nspringfield.local.toml\n"
	if err := os.WriteFile(gitignore, []byte(seeded), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	out, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude")
	if err != nil {
		t.Fatalf("init (default mode): %v\n%s", err, out)
	}

	after, err := os.ReadFile(gitignore)
	if err != nil {
		t.Fatalf("read .gitignore after init: %v", err)
	}
	if string(after) != seeded {
		t.Fatalf("default mode mutated tracked .gitignore\n--- before ---\n%s\n--- after ---\n%s", seeded, after)
	}

	if !strings.Contains(out, "tracked .gitignore already") {
		t.Errorf("expected pre-existing Springfield-block warning, got:\n%s", out)
	}
}

// TestInitDefaultNoWarnWithoutTrackedBlock guards the negative: a repo whose
// tracked .gitignore has no Springfield block gets no spurious warning.
func TestInitDefaultNoWarnWithoutTrackedBlock(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t) // seeds .gitignore with only bin/ + claude.pwd

	out, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude")
	if err != nil {
		t.Fatalf("init (default mode): %v\n%s", err, out)
	}

	if strings.Contains(out, "tracked .gitignore already") {
		t.Errorf("unexpected pre-existing-block warning, got:\n%s", out)
	}
}
