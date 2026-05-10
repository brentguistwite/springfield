package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// updateGolden lets the maintainer refresh testdata/lifecycle.golden.json after
// an intentional lifecycle change: `go test ./cmd/lifecycle-gen -update`.
var updateGolden = flag.Bool("update", false, "rewrite testdata/lifecycle.golden.json from the live walker output")

const goldenRel = "testdata/lifecycle.golden.json"

// TestGolden_LiveLifecycle pins the on-disk schema against the AST walker's
// view of the live conductor sources. Removing a PlanStatus const, dropping a
// `// lifecycle:edge` marker, or reordering enum members will fail this test.
// Update the golden with `-update` once the change is intentional.
func TestGolden_LiveLifecycle(t *testing.T) {
	lc, err := Build(conductorTypesPath(), transitionSourcePaths())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := MarshalDeterministic(lc)
	if err != nil {
		t.Fatalf("MarshalDeterministic: %v", err)
	}

	goldenPath := goldenAbsPath(t)
	if *updateGolden {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
		t.Logf("rewrote %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update to seed)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("lifecycle.json drift from golden — run `go test ./cmd/lifecycle-gen -update` if intentional.\n\n--- want (%s) ---\n%s\n--- got ---\n%s", goldenPath, string(want), string(got))
	}
}

// TestGolden_DeterministicAcrossRuns guards the wire-stability claim: marshaling
// the same lifecycle twice must produce byte-identical output.
func TestGolden_DeterministicAcrossRuns(t *testing.T) {
	lc, err := Build(conductorTypesPath(), transitionSourcePaths())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	a, err := MarshalDeterministic(lc)
	if err != nil {
		t.Fatalf("marshal a: %v", err)
	}
	b, err := MarshalDeterministic(lc)
	if err != nil {
		t.Fatalf("marshal b: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("non-deterministic marshal:\n--- a ---\n%s\n--- b ---\n%s", string(a), string(b))
	}
}

// TestGenerate_WritesFile exercises the end-to-end codegen path: the binary's
// generate() must materialize the file at the requested output path and the
// bytes must match the golden.
func TestGenerate_WritesFile(t *testing.T) {
	root := repoRootFromTest()
	outPath := filepath.Join(t.TempDir(), "nested", "lifecycle.json")
	if err := generate(root, outPath); err != nil {
		t.Fatalf("generate: %v", err)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	want, err := os.ReadFile(goldenAbsPath(t))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("generate() output drifted from golden:\n--- want ---\n%s\n--- got ---\n%s", string(want), string(got))
	}
}

// TestGolden_MissingStatusFailsLoudly proves the golden test would fail (with a
// useful error) if a PlanStatus const were removed: we simulate the deletion by
// pointing the walker at a synthetic types.go missing one of the live states.
func TestGolden_MissingStatusFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	// Synthetic conductor types.go with `failed` removed.
	synthetic := `package conductor

type PlanStatus string

const (
	StatusPending     PlanStatus = "pending"
	StatusRunning     PlanStatus = "running"
	StatusInterrupted PlanStatus = "interrupted"
	StatusCompleted   PlanStatus = "completed"
)
`
	syntheticPath := filepath.Join(dir, "types.go")
	if err := writeFile(syntheticPath, synthetic); err != nil {
		t.Fatal(err)
	}

	// Building with the synthetic types must fail loudly because live edges
	// reference `failed` and validation rejects unknown node ids.
	_, err := Build(syntheticPath, transitionSourcePaths())
	if err == nil {
		t.Fatal("want Build to fail when a status const is removed, got nil")
	}
}

func goldenAbsPath(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), goldenRel)
}

func repoRootFromTest() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
