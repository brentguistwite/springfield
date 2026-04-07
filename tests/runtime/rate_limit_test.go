package runtime_test

import (
	"errors"
	"testing"

	"springfield/internal/core/exec"
	"springfield/internal/core/runtime"
)

func TestIsRateLimitErrorMatchesErrorText(t *testing.T) {
	err := errors.New("Claude API error: rate limit exceeded, try again later")
	if !runtime.IsRateLimitError(err, nil) {
		t.Fatal("expected rate-limit match from error text")
	}
}

func TestIsRateLimitErrorMatchesEventStream(t *testing.T) {
	events := []exec.Event{
		{Type: exec.EventStderr, Data: "429 Too Many Requests"},
	}
	if !runtime.IsRateLimitError(nil, events) {
		t.Fatal("expected rate-limit match from stderr events")
	}
}

func TestIsRateLimitErrorRejectsGenericFailure(t *testing.T) {
	err := errors.New("process exited with code 1")
	if runtime.IsRateLimitError(err, nil) {
		t.Fatal("did not expect generic failure to count as rate-limit")
	}
}
