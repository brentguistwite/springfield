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

func TestGeminiValidateResultStreamJsonErrorEvent(t *testing.T) {
	v := geminiValidator(t)
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"type":"error","message":"boom"}`, Time: time.Now()},
	}
	err := v.ValidateResult(coreexec.Result{ExitCode: 0, Events: events})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected error containing boom, got %v", err)
	}
}

func TestGeminiValidateResultDetectsDeniedToolResult(t *testing.T) {
	v := geminiValidator(t)
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"type":"tool_result","is_error":true,"content":"request denied by policy"}`, Time: time.Now()},
	}
	err := v.ValidateResult(coreexec.Result{ExitCode: 0, Events: events})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected denied error, got %v", err)
	}
}

func TestGeminiValidateResultReturnsNilOnCleanRun(t *testing.T) {
	v := geminiValidator(t)
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"type":"init"}`, Time: time.Now()},
		{Type: coreexec.EventStdout, Data: `{"type":"message","text":"Working on it."}`, Time: time.Now()},
		{Type: coreexec.EventStdout, Data: `{"type":"tool_use","tool_name":"write_file"}`, Time: time.Now()},
		{Type: coreexec.EventStdout, Data: `{"type":"tool_result","is_error":false,"content":"ok"}`, Time: time.Now()},
		{Type: coreexec.EventStdout, Data: `{"type":"result"}`, Time: time.Now()},
	}
	if err := v.ValidateResult(coreexec.Result{ExitCode: 0, Events: events}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestGeminiValidateResultDetectsClarifyingQuestionWithoutWork(t *testing.T) {
	v := geminiValidator(t)
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"type":"message","text":"What file would you like me to edit?"}`, Time: time.Now()},
	}
	err := v.ValidateResult(coreexec.Result{ExitCode: 0, Events: events})
	if err == nil || !strings.Contains(err.Error(), "clarifying question") {
		t.Fatalf("expected clarifying-question error, got %v", err)
	}
}

func TestGeminiValidateResultAcceptsQuestionAfterDoingWork(t *testing.T) {
	v := geminiValidator(t)
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"type":"tool_use","tool_name":"write_file"}`, Time: time.Now()},
		{Type: coreexec.EventStdout, Data: `{"type":"tool_result","is_error":false,"content":"ok"}`, Time: time.Now()},
		{Type: coreexec.EventStdout, Data: `{"type":"message","text":"Do you want me to also update the tests?"}`, Time: time.Now()},
	}
	if err := v.ValidateResult(coreexec.Result{ExitCode: 0, Events: events}); err != nil {
		t.Fatalf("expected nil (work completed), got %v", err)
	}
}

func TestGeminiValidateResultExit1NoWorkReturnsError(t *testing.T) {
	v := geminiValidator(t)
	err := v.ValidateResult(coreexec.Result{ExitCode: 1})
	if err == nil || !strings.Contains(err.Error(), "exited with code 1") {
		t.Fatalf("expected exit-1 error, got %v", err)
	}
}
