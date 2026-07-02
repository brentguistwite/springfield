package planrun_test

import (
	"testing"

	"springfield/internal/features/conductor/planrun"
)

func TestResolveBatchBaseFlagWins(t *testing.T) {
	g := newFakeGit() // currentBranch = "main"
	got, err := planrun.ResolveBatchBase(g, "/root", "release/x", "develop")
	if err != nil {
		t.Fatalf("ResolveBatchBase: %v", err)
	}
	if got != "release/x" {
		t.Fatalf("flag base must win, got %q want release/x", got)
	}
}

func TestResolveBatchBaseConfigUsedWhenNoFlag(t *testing.T) {
	g := newFakeGit()
	got, err := planrun.ResolveBatchBase(g, "/root", "  ", "develop")
	if err != nil {
		t.Fatalf("ResolveBatchBase: %v", err)
	}
	if got != "develop" {
		t.Fatalf("config base must win over current, got %q want develop", got)
	}
}

func TestResolveBatchBaseFallsBackToCurrentBranch(t *testing.T) {
	g := newFakeGit() // currentBranch = "main"
	got, err := planrun.ResolveBatchBase(g, "/root", "", "")
	if err != nil {
		t.Fatalf("ResolveBatchBase: %v", err)
	}
	if got != "main" {
		t.Fatalf("must fall back to current branch, got %q want main", got)
	}
}

func TestResolveBatchBaseDetachedHeadRejected(t *testing.T) {
	g := newFakeGit()
	g.currentBranchOK = false // simulate detached HEAD
	_, err := planrun.ResolveBatchBase(g, "/root", "", "")
	if err == nil {
		t.Fatal("detached HEAD with no flag/config base must be rejected")
	}
	if pe := planrun.AsPreflight(err); pe == nil || pe.Tag != "preflight-detached-head" {
		t.Fatalf("want preflight-detached-head rejection, got %v", err)
	}
}

func TestResolveBatchBaseDetachedHeadIrrelevantWhenFlagGiven(t *testing.T) {
	g := newFakeGit()
	g.currentBranchOK = false // detached, but flag is provided
	got, err := planrun.ResolveBatchBase(g, "/root", "develop", "")
	if err != nil {
		t.Fatalf("flag base must short-circuit detached HEAD: %v", err)
	}
	if got != "develop" {
		t.Fatalf("got %q want develop", got)
	}
}
