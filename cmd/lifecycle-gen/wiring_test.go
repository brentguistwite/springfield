package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWiring_GoGenerateDirective pins the //go:generate directive that wires
// `go generate ./...` to this codegen binary. The directive must live in the
// same file that owns PlanStatus so a status change immediately surfaces as
// drift on the next generate run.
func TestWiring_GoGenerateDirective(t *testing.T) {
	root := repoRootFromTest()
	path := filepath.Join(root, conductorTypesRel)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(b)
	const want = "//go:generate go run springfield/cmd/lifecycle-gen"
	if !strings.Contains(src, want) {
		t.Fatalf("missing directive %q in %s", want, conductorTypesRel)
	}
}

// TestWiring_MakefileLifecycleTarget pins the top-level Makefile target so
// `make lifecycle` keeps working from the repo root.
func TestWiring_MakefileLifecycleTarget(t *testing.T) {
	root := repoRootFromTest()
	path := filepath.Join(root, "Makefile")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	src := string(b)
	if !strings.Contains(src, ".PHONY: lifecycle") {
		t.Fatal("Makefile missing `.PHONY: lifecycle`")
	}
	if !strings.Contains(src, "lifecycle:") {
		t.Fatal("Makefile missing `lifecycle:` target")
	}
	if !strings.Contains(src, "go run ./cmd/lifecycle-gen") {
		t.Fatal("Makefile lifecycle target must invoke `go run ./cmd/lifecycle-gen`")
	}
}
