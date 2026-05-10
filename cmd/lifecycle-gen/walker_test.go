package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractStatusNodes_LiveSource(t *testing.T) {
	got, err := ExtractStatusNodes(conductorTypesPath())
	if err != nil {
		t.Fatalf("ExtractStatusNodes: %v", err)
	}

	wantIDs := []string{"pending", "running", "interrupted", "completed", "failed"}
	if len(got) != len(wantIDs) {
		t.Fatalf("node count: want %d, got %d (%v)", len(wantIDs), len(got), got)
	}

	gotIDs := make(map[string]bool, len(got))
	for _, n := range got {
		gotIDs[n.ID] = true
		if n.Label == "" {
			t.Errorf("node %q has empty label", n.ID)
		}
	}
	for _, id := range wantIDs {
		if !gotIDs[id] {
			t.Errorf("missing node id %q in %v", id, got)
		}
	}
}

func TestExtractStatusNodes_MissingFile(t *testing.T) {
	_, err := ExtractStatusNodes(filepath.Join(t.TempDir(), "does-not-exist.go"))
	if err == nil {
		t.Fatal("want error for missing file, got nil")
	}
}

func TestExtractStatusNodes_NoEnum(t *testing.T) {
	dir := t.TempDir()
	src := `package conductor

type Other string

const (
	Foo Other = "foo"
)
`
	path := filepath.Join(dir, "types.go")
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	_, err := ExtractStatusNodes(path)
	if err == nil {
		t.Fatal("want error when PlanStatus enum absent, got nil")
	}
	if !strings.Contains(err.Error(), "PlanStatus") {
		t.Errorf("error should mention PlanStatus, got: %v", err)
	}
}

func TestExtractStatusNodes_TypedBlockAndStandalone(t *testing.T) {
	dir := t.TempDir()
	// Typed first spec + bare subsequent specs in the same block (Go inherits
	// type within a const block) plus a standalone typed const.
	src := `package conductor

type PlanStatus string

const (
	StatusA PlanStatus = "a"
	StatusB            = "b"
)

const StatusC PlanStatus = "c"
`
	path := filepath.Join(dir, "types.go")
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	got, err := ExtractStatusNodes(path)
	if err != nil {
		t.Fatalf("ExtractStatusNodes: %v", err)
	}
	want := map[string]bool{"a": true, "b": true, "c": true}
	if len(got) != len(want) {
		t.Fatalf("count: want %d got %d (%v)", len(want), len(got), got)
	}
	for _, n := range got {
		if !want[n.ID] {
			t.Errorf("unexpected id %q", n.ID)
		}
	}
}
