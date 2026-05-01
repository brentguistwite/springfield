package agents_test

import (
	"os/exec"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
	coreexec "springfield/internal/core/exec"
)

func TestAdaptersImplementErrorClassifier(t *testing.T) {
	registry := agents.NewRegistry(
		claude.New(exec.LookPath),
		codex.New(exec.LookPath),
		gemini.New(exec.LookPath),
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
			events:    []coreexec.Event{{Type: coreexec.EventStderr, Data: "429 Too Many Requests"}},
			exitCode:  1,
			wantClass: agents.ErrorClassRetryable,
		},
		{
			name:      "claude classifies unrecognized failure as fatal",
			agentID:   agents.AgentClaude,
			exitCode:  17,
			wantClass: agents.ErrorClassFatal,
		},
		{
			name:      "claude classifies missing cli as retryable",
			agentID:   agents.AgentClaude,
			exitCode:  1,
			err:       exec.ErrNotFound,
			wantClass: agents.ErrorClassRetryable,
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
			exitCode:  1,
			err:       exec.ErrNotFound,
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
			exitCode:  1,
			err:       exec.ErrNotFound,
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
