package agents_test

import (
	"errors"
	osexec "os/exec"
	"path/filepath"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
	coreexec "springfield/internal/core/exec"
)

func TestAdaptersImplementErrorClassifier(t *testing.T) {
	registry := agents.NewRegistry(
		claude.New(osexec.LookPath),
		codex.New(osexec.LookPath),
		gemini.New(osexec.LookPath),
	)

	tests := []struct {
		name      string
		agentID   agents.ID
		events    []coreexec.Event
		exitCode  int
		err       error
		wantClass agents.ErrorClass
	}{
		{
			name:      "claude classifies rate limit as retryable",
			agentID:   agents.AgentClaude,
			events:    []coreexec.Event{{Type: coreexec.EventStderr, Data: "rate_limit exceeded"}},
			exitCode:  1,
			err:       assertErr("claude failed"),
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "claude classifies authentication failure as retryable",
			agentID:   agents.AgentClaude,
			events:    []coreexec.Event{{Type: coreexec.EventStderr, Data: "authentication_error: unauthenticated 401"}},
			exitCode:  1,
			err:       assertErr("claude failed"),
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "claude classifies unrecognized failure as fatal",
			agentID:   agents.AgentClaude,
			exitCode:  17,
			err:       assertErr("claude failed"),
			wantClass: agents.ErrorClassFatal,
		},
		{
			name:      "claude classifies missing cli as retryable",
			agentID:   agents.AgentClaude,
			exitCode:  -1,
			err:       osexec.ErrNotFound,
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "claude classifies validator failure as fatal on clean exit",
			agentID:   agents.AgentClaude,
			exitCode:  0,
			err:       assertErr("validator rejected transcript"),
			wantClass: agents.ErrorClassFatal,
		},
		{
			name:      "codex classifies rate limit as retryable",
			agentID:   agents.AgentCodex,
			events:    []coreexec.Event{{Type: coreexec.EventStderr, Data: "quota exceeded"}},
			exitCode:  1,
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "codex classifies unrecognized failure as fatal",
			agentID:   agents.AgentCodex,
			exitCode:  17,
			wantClass: agents.ErrorClassFatal,
		},
		{
			name:      "codex classifies missing cli as retryable",
			agentID:   agents.AgentCodex,
			exitCode:  -1,
			err:       osexec.ErrNotFound,
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "gemini classifies rate limit as retryable",
			agentID:   agents.AgentGemini,
			events:    []coreexec.Event{{Type: coreexec.EventStderr, Data: "RESOURCE_EXHAUSTED: too many requests"}},
			exitCode:  1,
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "gemini classifies unrecognized failure as fatal",
			agentID:   agents.AgentGemini,
			exitCode:  17,
			wantClass: agents.ErrorClassFatal,
		},
		{
			name:      "gemini classifies missing cli as retryable",
			agentID:   agents.AgentGemini,
			exitCode:  -1,
			err:       osexec.ErrNotFound,
			wantClass: agents.ErrorClassRetryable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := registry.Resolve(agents.ResolveInput{ProjectDefault: tt.agentID})
			if err != nil {
				t.Fatalf("resolve adapter: %v", err)
			}

			classifier, ok := resolved.Adapter.(agents.ErrorClassifier)
			if !ok {
				t.Fatalf("adapter %q does not implement ErrorClassifier", tt.agentID)
			}

			got := classifier.ClassifyError(tt.events, tt.exitCode, tt.err)
			if got != tt.wantClass {
				t.Fatalf("ClassifyError() = %q, want %q", got, tt.wantClass)
			}
		})
	}
}

func TestCodexClassifyErrorUsesRetryableAndFatalRules(t *testing.T) {
	classifier, ok := codex.New(osexec.LookPath).(agents.ErrorClassifier)
	if !ok {
		t.Fatal("codex adapter does not implement ErrorClassifier")
	}

	tests := []struct {
		name      string
		events    []coreexec.Event
		exitCode  int
		err       error
		wantClass agents.ErrorClass
	}{
		{
			name:      "validator failure on clean exit is fatal",
			exitCode:  0,
			err:       assertErr("validator rejected transcript"),
			wantClass: agents.ErrorClassFatal,
		},
		{
			name:      "missing cli is retryable",
			exitCode:  -1,
			err:       osexec.ErrNotFound,
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "rate limit in codex command event is retryable",
			events:    append(loadFixtureEvents(t, filepath.Join("fixtures", "codex", "hard-error.json")), coreexec.Event{Type: coreexec.EventStdout, Data: `{"type":"item.completed","item":{"id":"item_9","type":"command_execution","command":"codex exec","aggregated_output":"HTTP 429: quota exceeded","exit_code":1,"status":"completed"}}`}),
			exitCode:  1,
			err:       assertErr("codex failed"),
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "authrequired stderr is retryable",
			events:    append(loadFixtureEvents(t, filepath.Join("fixtures", "codex", "success.json")), coreexec.Event{Type: coreexec.EventStderr, Data: `2026-04-08T16:34:12.577918Z ERROR rmcp::transport::worker: worker quit with fatal: Transport channel closed, when AuthRequired(AuthRequiredError { www_authenticate_header: "Bearer realm=\"OAuth\", error=\"invalid_token\", error_description=\"Missing or invalid access token\"" })`}),
			exitCode:  1,
			err:       assertErr("codex failed"),
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "unrecognized failure is fatal",
			exitCode:  17,
			err:       assertErr("codex failed"),
			wantClass: agents.ErrorClassFatal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifier.ClassifyError(tt.events, tt.exitCode, tt.err)
			if got != tt.wantClass {
				t.Fatalf("ClassifyError() = %q, want %q", got, tt.wantClass)
			}
		})
	}
}

func TestGeminiClassifyErrorValidatorFailureOnCleanExitIsFatal(t *testing.T) {
	classifier, ok := gemini.New(osexec.LookPath).(agents.ErrorClassifier)
	if !ok {
		t.Fatal("gemini adapter does not implement ErrorClassifier")
	}

	got := classifier.ClassifyError(nil, 0, assertErr("validator rejected transcript"))
	if got != agents.ErrorClassFatal {
		t.Fatalf("ClassifyError() = %q, want %q", got, agents.ErrorClassFatal)
	}
}

func TestGeminiClassifyErrorMissingCLIIsRetryable(t *testing.T) {
	classifier, ok := gemini.New(osexec.LookPath).(agents.ErrorClassifier)
	if !ok {
		t.Fatal("gemini adapter does not implement ErrorClassifier")
	}

	got := classifier.ClassifyError(nil, -1, osexec.ErrNotFound)
	if got != agents.ErrorClassRetryable {
		t.Fatalf("ClassifyError() = %q, want %q", got, agents.ErrorClassRetryable)
	}
}

func TestGeminiClassifyErrorRateLimitMessageEventIsRetryable(t *testing.T) {
	classifier, ok := gemini.New(osexec.LookPath).(agents.ErrorClassifier)
	if !ok {
		t.Fatal("gemini adapter does not implement ErrorClassifier")
	}

	events := append(
		loadFixtureEvents(t, filepath.Join("fixtures", "gemini", "hard-error.json")),
		coreexec.Event{Type: coreexec.EventStdout, Data: `{"type":"message","text":"RESOURCE_EXHAUSTED: too many requests"}`},
	)

	got := classifier.ClassifyError(events, 1, assertErr("gemini failed"))
	if got != agents.ErrorClassRetryable {
		t.Fatalf("ClassifyError() = %q, want %q", got, agents.ErrorClassRetryable)
	}
}

func TestGeminiClassifyErrorRateLimitStderrEventIsRetryable(t *testing.T) {
	classifier, ok := gemini.New(osexec.LookPath).(agents.ErrorClassifier)
	if !ok {
		t.Fatal("gemini adapter does not implement ErrorClassifier")
	}

	events := append(
		loadFixtureEvents(t, filepath.Join("fixtures", "gemini", "tool-error-all.json")),
		coreexec.Event{Type: coreexec.EventStderr, Data: "HTTP 429: quota exceeded"},
	)

	got := classifier.ClassifyError(events, 1, assertErr("gemini failed"))
	if got != agents.ErrorClassRetryable {
		t.Fatalf("ClassifyError() = %q, want %q", got, agents.ErrorClassRetryable)
	}
}

func TestGeminiClassifyErrorAuthenticationEventIsRetryable(t *testing.T) {
	classifier, ok := gemini.New(osexec.LookPath).(agents.ErrorClassifier)
	if !ok {
		t.Fatal("gemini adapter does not implement ErrorClassifier")
	}

	events := append(
		loadFixtureEvents(t, filepath.Join("fixtures", "gemini", "refusal-no-tools.json")),
		coreexec.Event{Type: coreexec.EventStdout, Data: `{"type":"message","text":"authentication failed: unauthenticated 401"}`},
	)

	got := classifier.ClassifyError(events, 1, assertErr("gemini failed"))
	if got != agents.ErrorClassRetryable {
		t.Fatalf("ClassifyError() = %q, want %q", got, agents.ErrorClassRetryable)
	}
}

func TestGeminiClassifyErrorToolResultAppHTTP500IsFatal(t *testing.T) {
	classifier, ok := gemini.New(osexec.LookPath).(agents.ErrorClassifier)
	if !ok {
		t.Fatal("gemini adapter does not implement ErrorClassifier")
	}

	events := append(
		loadFixtureEvents(t, filepath.Join("fixtures", "gemini", "tool-error-all.json")),
		coreexec.Event{Type: coreexec.EventStdout, Data: `{"type":"tool_result","tool_use_id":"call_03","is_error":true,"content":"app returned HTTP 500 while fetching report"}`},
	)

	got := classifier.ClassifyError(events, 1, assertErr("gemini failed"))
	if got != agents.ErrorClassFatal {
		t.Fatalf("ClassifyError() = %q, want %q", got, agents.ErrorClassFatal)
	}
}

func assertErr(msg string) error {
	return errors.New(msg)
}
