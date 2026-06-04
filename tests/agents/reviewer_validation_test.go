package agents_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	coreexec "springfield/internal/core/exec"
	"springfield/internal/testsupport/fixtures"
)

// BUG-4: the independent reviewer is legitimately tool-free (it reasons over an
// inline diff with tools forbidden). Under the strict implementer contract
// (requireToolAction=true) ValidateResult rejects such a run as "without taking
// action" and the batch aborts on a review that actually PASSED. The reviewer
// relaxation (requireToolAction=false) must accept a clean terminal-success
// transcript with zero tool calls; the verdict scanner judges substance.
//
// Both fixtures are REAL captures (cmd/capture-fixture) loaded through the
// production stdout reader, so the test input is byte-identical to what the
// runner parses in production — the gap that hid this bug.

func realcapture(t *testing.T, tool, scenario string) coreexec.Result {
	t.Helper()
	events := fixtures.LoadEvents(t, filepath.Join("..", "realcaptures", tool, scenario+".jsonl"))
	return coreexec.Result{ExitCode: 0, Events: events}
}

func TestClaudeValidateResultAcceptsToolFreeReviewer(t *testing.T) {
	v := mustValidator(t, claude.New(exec.LookPath))
	res := realcapture(t, "claude", "reviewer-verdict-pass-no-tools")

	// Reviewer mode: a clean result-success transcript with no tools is valid.
	if err := v.ValidateResult(res, false); err != nil {
		t.Fatalf("reviewer (requireToolAction=false) must accept a tool-free pass, got %v", err)
	}
	// Strict mode on the SAME transcript must still reject — pins the split and
	// proves the fixture really is the tool-free shape that broke the gate.
	if err := v.ValidateResult(res, true); err == nil {
		t.Fatal("implementer (requireToolAction=true) must reject a tool-free run")
	}
}

func TestCodexValidateResultAcceptsToolFreeReviewer(t *testing.T) {
	v := mustValidator(t, codex.New(exec.LookPath))
	res := realcapture(t, "codex", "reviewer-verdict-pass-no-tools")

	if err := v.ValidateResult(res, false); err != nil {
		t.Fatalf("reviewer (requireToolAction=false) must accept a tool-free pass, got %v", err)
	}
	if err := v.ValidateResult(res, true); err == nil {
		t.Fatal("implementer (requireToolAction=true) must reject a tool-free run")
	}
}
