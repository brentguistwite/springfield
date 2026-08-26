package agents_test

import (
	osexec "os/exec"
	"path/filepath"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/opencode"
	coreexec "springfield/internal/core/exec"
)

func opencodeClassifier(t *testing.T) agents.ErrorClassifier {
	t.Helper()
	c, ok := opencode.New(osexec.LookPath).(agents.ErrorClassifier)
	if !ok {
		t.Fatal("opencode adapter does not implement ErrorClassifier")
	}
	return c
}

func TestOpencodeClassifyError(t *testing.T) {
	tests := []struct {
		name      string
		events    []coreexec.Event
		exitCode  int
		err       error
		wantClass agents.ErrorClass
	}{
		{
			name:      "exit 0 is fatal",
			exitCode:  0,
			err:       assertErr("validator rejected transcript"),
			wantClass: agents.ErrorClassFatal,
		},
		{
			name:      "clean exit is fatal even when error text carries a retryable needle",
			exitCode:  0,
			err:       assertErr("rate limit hit while validating"),
			wantClass: agents.ErrorClassFatal,
		},
		{
			name:      "missing cli is retryable",
			exitCode:  -1,
			err:       osexec.ErrNotFound,
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "rate limit stderr is retryable",
			events:    []coreexec.Event{{Type: coreexec.EventStderr, Data: "HTTP 429: rate limit exceeded"}},
			exitCode:  1,
			err:       assertErr("opencode failed"),
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "quota exceeded stderr is retryable",
			events:    []coreexec.Event{{Type: coreexec.EventStderr, Data: "quota exceeded for this project"}},
			exitCode:  1,
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "timeout process error is retryable",
			exitCode:  1,
			err:       assertErr("request timed out talking to provider"),
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "unauthorized stderr is retryable",
			events:    []coreexec.Event{{Type: coreexec.EventStderr, Data: "401 unauthorized: invalid api key"}},
			exitCode:  1,
			wantClass: agents.ErrorClassRetryable,
		},
		{
			// Parity with gemini (geminiRetryableNeedles carries the bare
			// "authentication" needle): "authentication failed" provider
			// errors must retry, not go fatal.
			name:      "authentication failed stderr is retryable",
			events:    []coreexec.Event{{Type: coreexec.EventStderr, Data: "provider error: authentication failed"}},
			exitCode:  1,
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "overloaded stderr is retryable",
			events:    []coreexec.Event{{Type: coreexec.EventStderr, Data: "provider overloaded, try again"}},
			exitCode:  1,
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "connection refused stderr is retryable",
			events:    []coreexec.Event{{Type: coreexec.EventStderr, Data: "dial tcp: connection refused"}},
			exitCode:  1,
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name: "top-level error event message is scanned",
			events: append(
				loadFixtureEvents(t, filepath.Join("fixtures", "opencode", "success.jsonl")),
				coreexec.Event{Type: coreexec.EventStdout, Data: `{"type":"error","timestamp":1787450359518,"sessionID":"ses_x","error":{"name":"UnknownError","data":{"message":"model unavailable: 503 service unavailable","ref":"err_x"}}}`},
			),
			exitCode:  1,
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "unrecognized failure is fatal",
			exitCode:  17,
			err:       assertErr("opencode failed"),
			wantClass: agents.ErrorClassFatal,
		},
		{
			name:      "hard-error capture without retryable needles is fatal",
			events:    loadFixtureEvents(t, filepath.Join("fixtures", "opencode", "hard-error.jsonl")),
			exitCode:  1,
			wantClass: agents.ErrorClassFatal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := opencodeClassifier(t).ClassifyError(tt.events, tt.exitCode, tt.err)
			if got != tt.wantClass {
				t.Fatalf("ClassifyError() = %q, want %q", got, tt.wantClass)
			}
		})
	}
}
