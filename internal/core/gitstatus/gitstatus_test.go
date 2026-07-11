package gitstatus_test

import (
	"testing"

	"springfield/internal/core/gitstatus"
)

func TestDirtyIgnoresSpringfieldOwnedPaths(t *testing.T) {
	cases := []struct {
		name      string
		porcelain string
		want      bool
	}{
		{"clean tree", "", false},
		{"whitespace only", "   \n", false},

		// Control-plane + worktree bookkeeping.
		{"untracked springfield log", "?? .springfield/logs/plan-run.log", false},
		{"modified springfield state", " M .springfield/execution/state.json", false},
		{"untracked worktree dir", "?? .worktrees/feature-a/", false},
		{"renamed inside springfield", "R  .springfield/old.json -> .springfield/new.json", false},

		// Generated config the init/team-safe flow owns.
		{"untracked springfield.toml", "?? springfield.toml", false},
		{"modified springfield.toml", " M springfield.toml", false},
		{"untracked local override", "?? springfield.local.toml", false},
		{"modified local override", " M springfield.local.toml", false},
		{"timestamped backup", "?? springfield.toml.bak-20260710T120000", false},

		// Real source changes still count.
		{"untracked source file", "?? src/main.go", true},
		{"modified source file", " M README.md", true},
		{"a lookalike is not owned", "?? springfield.tomlish", true},
		{"deeper toml is not the exact file", "?? config/springfield.toml", true},

		// Mixed: one real change among owned changes is still dirty.
		{"owned plus real", "?? .springfield/logs/x\n M README.md", true},
		{"only owned across lines", "?? .springfield/logs/x\n M springfield.toml", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gitstatus.Dirty(tc.porcelain); got != tc.want {
				t.Fatalf("Dirty(%q) = %v, want %v", tc.porcelain, got, tc.want)
			}
		})
	}
}
