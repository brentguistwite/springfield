package cost

import (
	"io/fs"
	"os"
	"testing"
)

// countWalkErrAsSkipped decides whether a WalkDir entry error inflates
// SkippedFiles. The readdir→stat race with a concurrent sibling's temp+rename
// writes can't be reproduced deterministically through the real filesystem,
// so the classification is tested directly.
func TestCountWalkErrAsSkipped(t *testing.T) {
	cases := []struct {
		name string
		path string
		err  error
		want bool
	}{
		// A concurrently-running sibling plan's temp file listed then
		// renamed away mid-walk: transient churn, nothing was lost.
		{"transient non-cost entry vanished", "/x/evidence/iter-1/cost.json.tmp123", fs.ErrNotExist, false},
		{"transient dir vanished", "/x/evidence/iter-1", fs.ErrNotExist, false},
		// A cost.json that vanished is a genuine potential under-count.
		{"cost.json vanished", "/x/evidence/iter-1/cost.json", fs.ErrNotExist, true},
		// Any non-ErrNotExist error stays a skip regardless of name.
		{"permission denied on entry", "/x/evidence/iter-1/cost.json.tmp123", os.ErrPermission, true},
		{"permission denied on cost.json", "/x/evidence/iter-1/cost.json", os.ErrPermission, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countWalkErrAsSkipped(tc.path, tc.err); got != tc.want {
				t.Errorf("countWalkErrAsSkipped(%q, %v) = %v, want %v", tc.path, tc.err, got, tc.want)
			}
		})
	}
}
