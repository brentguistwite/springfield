package agents_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/opencode"
	coreexec "springfield/internal/core/exec"
)

func opencodeValidator(t *testing.T) agents.ResultValidator {
	t.Helper()
	a := opencode.New(exec.LookPath)
	v, ok := a.(agents.ResultValidator)
	if !ok {
		t.Fatal("opencode adapter does not implement ResultValidator")
	}
	return v
}

// A clean success transcript from a REAL capture (fixtures are verbatim
// opencode run --format json lines): at least one tool_use part with
// state.status "completed" is the positive completion signal.
func TestOpencodeValidateResultReturnsNilOnCleanRun(t *testing.T) {
	v := opencodeValidator(t)
	events := loadFixtureEvents(t, filepath.Join("fixtures", "opencode", "success.jsonl"))
	if err := v.ValidateResult(coreexec.Result{ExitCode: 0, Events: events}, true); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// A text-only run (REAL capture) never carries a tool_use event — under the
// positive-signal contract it fails no matter what the text says.
func TestOpencodeValidateResultRejectsTextOnlyRun(t *testing.T) {
	v := opencodeValidator(t)
	events := loadFixtureEvents(t, filepath.Join("fixtures", "opencode", "text-only.jsonl"))
	err := v.ValidateResult(coreexec.Result{ExitCode: 0, Events: events}, true)
	if err == nil {
		t.Fatal("expected non-nil error for text-only run")
	}
	if !strings.Contains(err.Error(), "tool work") && !strings.Contains(err.Error(), "tool") {
		t.Fatalf("error should mention missing tool work, got %q", err.Error())
	}
}

// Any non-zero exit fails before stream inspection; when the transcript
// carries a top-level {"type":"error"} event (REAL capture of --model
// nonexistent/nope), its message is surfaced for the operator.
func TestOpencodeValidateResultSurfacesErrorEventMessage(t *testing.T) {
	v := opencodeValidator(t)
	events := loadFixtureEvents(t, filepath.Join("fixtures", "opencode", "hard-error.jsonl"))
	err := v.ValidateResult(coreexec.Result{ExitCode: 1, Events: events}, true)
	if err == nil {
		t.Fatal("expected non-nil error for exit-1 run")
	}
	if !strings.Contains(err.Error(), "Unexpected server error") {
		t.Fatalf("expected decoded error-event message to be surfaced, got %q", err.Error())
	}
}

// A tool_use whose part.state.status is "error" is NOT a positive signal:
// an implementer run where every tool call failed must fail validation.
func TestOpencodeValidateResultRejectsAllErroredToolCalls(t *testing.T) {
	v := opencodeValidator(t)
	events := loadFixtureEvents(t, filepath.Join("fixtures", "opencode", "tool-error.jsonl"))
	err := v.ValidateResult(coreexec.Result{ExitCode: 0, Events: events}, true)
	if err == nil || !strings.Contains(err.Error(), "tool work") {
		t.Fatalf("expected without-tool-work error, got %v", err)
	}
}

// Reviewer mode (requireToolAction=false): a clean exit with ZERO tool calls
// is a valid completion — the verdict scanner judges substance.
func TestOpencodeValidateResultReviewerModeAcceptsToolFreeRun(t *testing.T) {
	v := opencodeValidator(t)
	events := loadFixtureEvents(t, filepath.Join("fixtures", "opencode", "text-only.jsonl"))
	if err := v.ValidateResult(coreexec.Result{ExitCode: 0, Events: events}, false); err != nil {
		t.Fatalf("expected nil in reviewer mode, got %v", err)
	}
}

// Reviewer mode does NOT relax the exit-code guard: non-zero still fails.
func TestOpencodeValidateResultReviewerModeStillFailsNonZeroExit(t *testing.T) {
	v := opencodeValidator(t)
	events := loadFixtureEvents(t, filepath.Join("fixtures", "opencode", "hard-error.jsonl"))
	err := v.ValidateResult(coreexec.Result{ExitCode: 1, Events: events}, false)
	if err == nil {
		t.Fatal("expected non-nil error for exit-1 run in reviewer mode")
	}
}

// Non-JSON stdout noise must be ignored by the decoder, not fail validation.
func TestOpencodeValidateResultIgnoresNonJSONLines(t *testing.T) {
	v := opencodeValidator(t)
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: "not json at all"},
		{
			Type: coreexec.EventStdout,
			Data: `{"type":"tool_use","timestamp":1787450340000,"sessionID":"ses_x","part":{"id":"prt_1","type":"tool","tool":"write","state":{"status":"completed","input":{"filePath":"/tmp/x","content":"hi"},"output":"ok"},"time":null}}`,
		},
	}
	if err := v.ValidateResult(coreexec.Result{ExitCode: 0, Events: events}, true); err != nil {
		t.Fatalf("expected nil (completed tool_use present), got %v", err)
	}
}
