package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractEdges_LiveSource(t *testing.T) {
	got, err := ExtractEdges(transitionSourcePaths(), validNodeIDs())
	if err != nil {
		t.Fatalf("ExtractEdges: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("want at least one edge from live source, got 0")
	}

	// Acceptance criteria: required transitions must be present.
	required := []struct {
		from, to string
		kind     EdgeKind
	}{
		{"pending", "running", EdgeNormal},
		{"running", "completed", EdgeNormal},
		{"running", "failed", EdgeFailure},
		{"failed", "running", EdgeRecovery},
	}
	for _, r := range required {
		if !hasEdge(got, r.from, r.to, r.kind) {
			t.Errorf("missing required edge %s -> %s (%s); got %v", r.from, r.to, r.kind, got)
		}
	}

	// At least one fallback-kind edge.
	var fallbackCount int
	for _, e := range got {
		if e.Kind == EdgeFallback {
			fallbackCount++
		}
	}
	if fallbackCount == 0 {
		t.Errorf("want at least one fallback edge; got %v", got)
	}
}

func TestExtractEdges_NestedControlFlow(t *testing.T) {
	dir := t.TempDir()
	src := `package x

func F() {
	if true {
		// lifecycle:edge from=pending to=running kind=normal
		_ = 1
		switch 1 {
		case 1:
			for {
				select {
				case <-make(chan int):
					// lifecycle:edge from=running to=failed kind=failure label="deeply nested"
					_ = 2
				}
				break
			}
		}
	}
}
`
	path := filepath.Join(dir, "f.go")
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	got, err := ExtractEdges([]string{path}, map[string]bool{"pending": true, "running": true, "failed": true})
	if err != nil {
		t.Fatalf("ExtractEdges: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 edges, got %d (%v)", len(got), got)
	}
	if !hasEdge(got, "pending", "running", EdgeNormal) {
		t.Errorf("missing pending->running normal: %v", got)
	}
	if !hasEdge(got, "running", "failed", EdgeFailure) {
		t.Errorf("missing running->failed failure: %v", got)
	}
	for _, e := range got {
		if e.From == "running" && e.To == "failed" && e.Label != "deeply nested" {
			t.Errorf("label not parsed; got %q", e.Label)
		}
	}
}

func TestExtractEdges_NoMarkers(t *testing.T) {
	dir := t.TempDir()
	src := "package x\n\nfunc F() {}\n"
	path := filepath.Join(dir, "f.go")
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	_, err := ExtractEdges([]string{path}, map[string]bool{"pending": true})
	if err == nil {
		t.Fatal("want error when no markers found, got nil")
	}
	if !strings.Contains(err.Error(), "lifecycle:edge") {
		t.Errorf("error should mention lifecycle:edge marker; got %v", err)
	}
}

func TestExtractEdges_UnknownKind(t *testing.T) {
	dir := t.TempDir()
	src := `package x
// lifecycle:edge from=a to=b kind=bogus
func F() {}
`
	path := filepath.Join(dir, "f.go")
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	_, err := ExtractEdges([]string{path}, map[string]bool{"a": true, "b": true})
	if err == nil {
		t.Fatal("want error for unknown kind, got nil")
	}
	if !strings.Contains(err.Error(), "kind") {
		t.Errorf("error should mention bad kind; got %v", err)
	}
}

func TestExtractEdges_UnknownNode(t *testing.T) {
	dir := t.TempDir()
	src := `package x
// lifecycle:edge from=ghost to=running kind=normal
func F() {}
`
	path := filepath.Join(dir, "f.go")
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	_, err := ExtractEdges([]string{path}, map[string]bool{"running": true})
	if err == nil {
		t.Fatal("want error when 'from' references unknown node, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should mention unknown id; got %v", err)
	}
}

func TestExtractEdges_MalformedMarker(t *testing.T) {
	dir := t.TempDir()
	src := `package x
// lifecycle:edge from=a kind=normal
func F() {}
`
	path := filepath.Join(dir, "f.go")
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	_, err := ExtractEdges([]string{path}, map[string]bool{"a": true})
	if err == nil {
		t.Fatal("want error for missing 'to', got nil")
	}
}

func TestExtractEdges_QuotedLabelWithSpaces(t *testing.T) {
	dir := t.TempDir()
	src := `package x
// lifecycle:edge from=a to=b kind=fallback label="agent retry chain"
func F() {}
`
	path := filepath.Join(dir, "f.go")
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	got, err := ExtractEdges([]string{path}, map[string]bool{"a": true, "b": true})
	if err != nil {
		t.Fatalf("ExtractEdges: %v", err)
	}
	if len(got) != 1 || got[0].Label != "agent retry chain" {
		t.Fatalf("want single edge with label \"agent retry chain\"; got %v", got)
	}
}

func TestExtractEdges_DeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	srcA := `package x
// lifecycle:edge from=a to=b kind=normal
// lifecycle:edge from=b to=c kind=failure
func F() {}
`
	srcB := `package x
// lifecycle:edge from=c to=a kind=recovery
func G() {}
`
	pathA := filepath.Join(dir, "a.go")
	pathB := filepath.Join(dir, "b.go")
	if err := writeFile(pathA, srcA); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(pathB, srcB); err != nil {
		t.Fatal(err)
	}
	nodes := map[string]bool{"a": true, "b": true, "c": true}
	first, err := ExtractEdges([]string{pathA, pathB}, nodes)
	if err != nil {
		t.Fatalf("ExtractEdges first: %v", err)
	}
	second, err := ExtractEdges([]string{pathA, pathB}, nodes)
	if err != nil {
		t.Fatalf("ExtractEdges second: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("len mismatch: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("idx %d: %v vs %v", i, first[i], second[i])
		}
	}
}

func hasEdge(edges []Edge, from, to string, kind EdgeKind) bool {
	for _, e := range edges {
		if e.From == from && e.To == to && e.Kind == kind {
			return true
		}
	}
	return false
}

func validNodeIDs() map[string]bool {
	nodes, err := ExtractStatusNodes(conductorTypesPath())
	if err != nil {
		return nil
	}
	out := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		out[n.ID] = true
	}
	return out
}
