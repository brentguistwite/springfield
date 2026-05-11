package conductor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"
)

// TestLifecycleNodeCountTripWire pins the count of declared status consts in
// types.go (PlanStatus, QueueStatus, MergeStatus) against the EXPECTED_*_NODE_COUNT
// literals in flowchart/src/data/lifecycle.ts. Drift in either direction fails
// the test with a message naming the machine that disagrees.
//
// This is the entire drift defense — no codegen, no AST walking of transitions.
func TestLifecycleNodeCountTripWire(t *testing.T) {
	goCounts := countStatusConsts(t, locateFile(t, "types.go"))

	tsBytes, err := os.ReadFile(locateLifecycleTS(t))
	if err != nil {
		t.Fatalf("read lifecycle.ts: %v", err)
	}
	tsCounts := parseExpectedCounts(t, string(tsBytes))

	for _, m := range []struct {
		name string
		key  string
	}{
		{"plan", "PLAN"},
		{"queue", "QUEUE"},
		{"merge", "MERGE"},
	} {
		got := goCounts[m.name]
		want, ok := tsCounts[m.key]
		if !ok {
			t.Fatalf("%s machine: EXPECTED_%s_NODE_COUNT not found in flowchart/src/data/lifecycle.ts", m.name, m.key)
		}
		if got != want {
			t.Fatalf("%s machine: %d consts in Go but flowchart says %d — update flowchart/src/data/lifecycle.ts after changing %sStatus",
				m.name, got, want, capitalize(m.name))
		}
	}
}

func locateFile(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), name)
}

func locateLifecycleTS(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	// .../internal/features/conductor/lifecycle_count_test.go → repo root
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	return filepath.Join(repoRoot, "flowchart", "src", "data", "lifecycle.ts")
}

// countStatusConsts AST-parses the Go file and tallies consts whose declared
// type is PlanStatus / QueueStatus / MergeStatus. Constants in a const block
// without an explicit type per-spec inherit the prior spec's type (iota-style),
// so we propagate the last seen type within each GenDecl.
func countStatusConsts(t *testing.T, path string) map[string]int {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	counts := map[string]int{"plan": 0, "queue": 0, "merge": 0}
	typeKey := map[string]string{
		"PlanStatus":  "plan",
		"QueueStatus": "queue",
		"MergeStatus": "merge",
	}

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		var currentTypeName string
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if vs.Type != nil {
				if id, ok := vs.Type.(*ast.Ident); ok {
					currentTypeName = id.Name
				} else {
					currentTypeName = ""
				}
			}
			key, tracked := typeKey[currentTypeName]
			if !tracked {
				continue
			}
			counts[key] += len(vs.Names)
		}
	}
	return counts
}

// Tolerates an optional trailing semicolon and an optional `// comment` after
// the literal, so future maintainers can annotate without silently breaking
// the drift check.
var expectedCountRe = regexp.MustCompile(`(?m)^export const EXPECTED_(PLAN|QUEUE|MERGE)_NODE_COUNT\s*=\s*(\d+)\s*;?\s*(?://.*)?$`)

func parseExpectedCounts(t *testing.T, ts string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, m := range expectedCountRe.FindAllStringSubmatch(ts, -1) {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatalf("bad numeric literal %q: %v", m[2], err)
		}
		out[m[1]] = n
	}
	return out
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:]
}
