package agents_test

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/gemini"
	coreexec "springfield/internal/core/exec"
)

func geminiValidator(t *testing.T) agents.ResultValidator {
	t.Helper()
	a := gemini.New(exec.LookPath)
	v, ok := a.(agents.ResultValidator)
	if !ok {
		t.Fatal("gemini adapter does not implement ResultValidator")
	}
	return v
}

func TestGeminiValidateResultTurnLimitExceeded(t *testing.T) {
	v := geminiValidator(t)
	err := v.ValidateResult(coreexec.Result{ExitCode: 53})
	if err == nil || !strings.Contains(err.Error(), "turn limit") {
		t.Fatalf("expected turn-limit error, got %v", err)
	}
}

func TestGeminiValidateResultInputError(t *testing.T) {
	v := geminiValidator(t)
	err := v.ValidateResult(coreexec.Result{ExitCode: 42})
	if err == nil || !strings.Contains(err.Error(), "rejected input") {
		t.Fatalf("expected input error, got %v", err)
	}
}

// Under the positive-signal contract, any non-zero exit fails before stream
// inspection — there is no need to scan for an "error" event marker.
func TestGeminiValidateResultExit1NoWorkReturnsError(t *testing.T) {
	v := geminiValidator(t)
	err := v.ValidateResult(coreexec.Result{ExitCode: 1})
	if err == nil || !strings.Contains(err.Error(), "non-zero code 1") {
		t.Fatalf("expected exit-1 error, got %v", err)
	}
}

// A clean success transcript: a tool_use paired with a non-error tool_result
// is the only thing the contract treats as a positive completion signal.
func TestGeminiValidateResultReturnsNilOnCleanRun(t *testing.T) {
	v := geminiValidator(t)
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"type":"init"}`, Time: time.Now()},
		{Type: coreexec.EventStdout, Data: `{"type":"message","text":"Working on it."}`, Time: time.Now()},
		{Type: coreexec.EventStdout, Data: `{"type":"tool_use","id":"call_01","tool_name":"write_file"}`, Time: time.Now()},
		{Type: coreexec.EventStdout, Data: `{"type":"tool_result","tool_use_id":"call_01","is_error":false,"content":"ok"}`, Time: time.Now()},
		{Type: coreexec.EventStdout, Data: `{"type":"result"}`, Time: time.Now()},
	}
	if err := v.ValidateResult(coreexec.Result{ExitCode: 0, Events: events}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// A text-only run with no successful tool_result fails the contract — no
// matter what the message says (clarifying question, refusal, plain text).
func TestGeminiValidateResultRejectsTextOnlyRun(t *testing.T) {
	v := geminiValidator(t)
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"type":"message","text":"What file would you like me to edit?"}`, Time: time.Now()},
	}
	err := v.ValidateResult(coreexec.Result{ExitCode: 0, Events: events})
	if err == nil || !strings.Contains(err.Error(), "without completing tool work") {
		t.Fatalf("expected without-completing error, got %v", err)
	}
}

// Once a tool_use/tool_result success pair appears, downstream chatter is
// ignored — the positive signal is what matters.
func TestGeminiValidateResultAcceptsQuestionAfterDoingWork(t *testing.T) {
	v := geminiValidator(t)
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"type":"tool_use","id":"call_01","tool_name":"write_file"}`, Time: time.Now()},
		{Type: coreexec.EventStdout, Data: `{"type":"tool_result","tool_use_id":"call_01","is_error":false,"content":"ok"}`, Time: time.Now()},
		{Type: coreexec.EventStdout, Data: `{"type":"message","text":"Do you want me to also update the tests?"}`, Time: time.Now()},
	}
	if err := v.ValidateResult(coreexec.Result{ExitCode: 0, Events: events}); err != nil {
		t.Fatalf("expected nil (work completed), got %v", err)
	}
}

// All-tools-errored: every tool_result reports is_error=true. No positive
// signal exists, so the contract rejects.
func TestGeminiValidateResultRejectsAllErroredToolResults(t *testing.T) {
	v := geminiValidator(t)
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"type":"tool_use","id":"call_01","tool_name":"read_file"}`, Time: time.Now()},
		{Type: coreexec.EventStdout, Data: `{"type":"tool_result","tool_use_id":"call_01","is_error":true,"content":"file not found"}`, Time: time.Now()},
	}
	err := v.ValidateResult(coreexec.Result{ExitCode: 0, Events: events})
	if err == nil || !strings.Contains(err.Error(), "without completing tool work") {
		t.Fatalf("expected without-completing error, got %v", err)
	}
}
