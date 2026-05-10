package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestLifecycle_RoundTrip(t *testing.T) {
	orig := Lifecycle{
		Nodes: []Node{
			{ID: "pending", Label: "Pending"},
			{ID: "running", Label: "Running"},
			{ID: "failed", Label: "Failed"},
			{ID: "done", Label: "Done"},
		},
		Edges: []Edge{
			{From: "pending", To: "running", Kind: EdgeNormal, Label: "start"},
			{From: "running", To: "running", Kind: EdgeFallback, Label: "agent fallback"},
			{From: "running", To: "failed", Kind: EdgeFailure, Label: "error"},
			{From: "failed", To: "running", Kind: EdgeRecovery, Label: "recover"},
		},
	}

	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Lifecycle
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !reflect.DeepEqual(orig, got) {
		t.Fatalf("round-trip mismatch:\n want %#v\n  got %#v", orig, got)
	}
}

func TestEdgeKind_Valid(t *testing.T) {
	valid := []EdgeKind{EdgeNormal, EdgeFallback, EdgeFailure, EdgeRecovery}
	for _, k := range valid {
		if !k.Valid() {
			t.Errorf("expected %q valid", k)
		}
	}
}

func TestEdgeKind_Invalid(t *testing.T) {
	cases := []EdgeKind{"", "bogus", "Normal", "NORMAL"}
	for _, k := range cases {
		if k.Valid() {
			t.Errorf("expected %q invalid", k)
		}
	}
}

func TestEdgeKind_JSONStringForm(t *testing.T) {
	e := Edge{From: "a", To: "b", Kind: EdgeFallback}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"from":"a","to":"b","kind":"fallback"}`
	if string(b) != want {
		t.Fatalf("wire form\n want %s\n  got %s", want, string(b))
	}
}
