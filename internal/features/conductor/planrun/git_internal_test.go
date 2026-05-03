package planrun

import "testing"

func TestLineIsSpringfieldOwnedRecognizesControlPaths(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"untracked springfield log", "?? .springfield/logs/plan-run.log", true},
		{"modified springfield state", " M .springfield/execution/state.json", true},
		{"untracked worktree dir", "?? .worktrees/feature-a/", true},
		{"renamed inside springfield", "R  .springfield/old.json -> .springfield/new.json", true},
		{"untracked source file", "?? src/main.go", false},
		{"modified source file", " M README.md", false},
		{"empty", "", false},
		{"too short", "?", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lineIsSpringfieldOwned(tc.line); got != tc.want {
				t.Fatalf("line %q: got %v want %v", tc.line, got, tc.want)
			}
		})
	}
}
