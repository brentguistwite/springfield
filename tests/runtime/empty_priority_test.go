package runtime_test

import (
	"os/exec"
	"strings"
	"testing"

	"springfield/internal/core/config"
	"springfield/internal/features/execution"
)

// TestRuntimeRunnerErrorsOnEmptyPriority verifies that constructing a
// runtime runner against a project whose agent_priority is empty fails
// with a clear error pointing the user back at `springfield init`. Empty
// priority is a valid unconfigured state per the config schema, so the
// runtime — not the loader — must reject it.
func TestRuntimeRunnerErrorsOnEmptyPriority(t *testing.T) {
	dir := t.TempDir()
	if _, err := config.Init(dir, []string{}, config.InitOptions{}); err != nil {
		t.Fatalf("config.Init: %v", err)
	}

	_, err := execution.NewRuntimeRunner(dir, exec.LookPath, nil)
	if err == nil {
		t.Fatal("expected error on empty agent_priority, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "agent_priority") || !strings.Contains(msg, "init") {
		t.Fatalf("error should reference agent_priority and init, got: %v", err)
	}
}
