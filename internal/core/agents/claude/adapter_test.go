package claude

import (
	"testing"

	coreexec "springfield/internal/core/exec"
)

func TestClaudeRetryableEvent(t *testing.T) {
	cases := []struct {
		name  string
		event coreexec.Event
		want  bool
	}{
		{
			name: "stdout rate_limit_event JSON is retryable",
			event: coreexec.Event{
				Type: coreexec.EventStdout,
				Data: `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1799999999}}`,
			},
			want: true,
		},
		{
			name: "stdout result with api_error_status is retryable",
			event: coreexec.Event{
				Type: coreexec.EventStdout,
				Data: `{"type":"result","is_error":true,"api_error_status":429}`,
			},
			want: true,
		},
		{
			name: "stdout result with api_error_status 503 is retryable",
			event: coreexec.Event{
				Type: coreexec.EventStdout,
				Data: `{"type":"result","is_error":true,"api_error_status":503}`,
			},
			want: true,
		},
		{
			name: "stderr rate limit text is retryable (regression)",
			event: coreexec.Event{
				Type: coreexec.EventStderr,
				Data: "Error: 429 too many requests",
			},
			want: true,
		},
		{
			name: "stderr overloaded text is retryable (regression)",
			event: coreexec.Event{
				Type: coreexec.EventStderr,
				Data: "overloaded_error: service is overloaded",
			},
			want: true,
		},
		{
			name: "stdout plain success result is not retryable (regression)",
			event: coreexec.Event{
				Type: coreexec.EventStdout,
				Data: `{"type":"result","is_error":false,"result":"done"}`,
			},
			want: false,
		},
		{
			name: "stderr benign text is not retryable (regression)",
			event: coreexec.Event{
				Type: coreexec.EventStderr,
				Data: "compiling sources...",
			},
			want: false,
		},
		{
			name: "empty event is not retryable",
			event: coreexec.Event{
				Type: coreexec.EventStdout,
				Data: "",
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeRetryableEvent(tc.event); got != tc.want {
				t.Fatalf("claudeRetryableEvent(%q on %s) = %v, want %v",
					tc.event.Data, tc.event.Type, got, tc.want)
			}
		})
	}
}
