package storage

import (
	"path/filepath"
	"testing"
)

func TestWorkPaths(t *testing.T) {
	rt, err := FromRoot(t.TempDir())
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}

	work, err := rt.Work("demo")
	if err != nil {
		t.Fatalf("build work paths: %v", err)
	}

	if got := rt.WorkIndexPath(); got != filepath.Join(rt.Dir, "work", "index.json") {
		t.Fatalf("work index path = %q", got)
	}

	if got := work.DirPath(); got != filepath.Join(rt.Dir, "work", "demo") {
		t.Fatalf("work dir path = %q", got)
	}

	if got := work.RequestPath(); got != filepath.Join(rt.Dir, "work", "demo", "request.md") {
		t.Fatalf("request path = %q", got)
	}

	if got := work.WorkstreamPath("backend"); got != filepath.Join(rt.Dir, "work", "demo", "workstream-backend.json") {
		t.Fatalf("workstream path = %q", got)
	}

	if got := work.RunStatePath(); got != filepath.Join(rt.Dir, "work", "demo", "run-state.json") {
		t.Fatalf("run-state path = %q", got)
	}
}

func TestWorkRejectsEscapingIDs(t *testing.T) {
	rt, err := FromRoot(t.TempDir())
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}

	for _, id := range []string{"", ".", "..", "../demo", "demo/../../escape"} {
		if _, err := rt.Work(id); err == nil {
			t.Fatalf("expected invalid work id %q to be rejected", id)
		}
	}
}
