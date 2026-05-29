package exec_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"springfield/internal/core/exec"
)

func TestRunCapturesStdoutEvents(t *testing.T) {
	result := exec.Run(context.Background(), exec.Command{
		Name: "echo",
		Args: []string{"hello world"},
	}, nil)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}

	stdout := filterEvents(result.Events, exec.EventStdout)
	if len(stdout) == 0 {
		t.Fatal("expected at least one stdout event")
	}
	if stdout[0].Data != "hello world" {
		t.Errorf("expected 'hello world', got %q", stdout[0].Data)
	}
}

func TestRunReportsNonZeroExitCode(t *testing.T) {
	result := exec.Run(context.Background(), exec.Command{
		Name: "sh",
		Args: []string{"-c", "exit 42"},
	}, nil)

	if result.ExitCode != 42 {
		t.Fatalf("expected exit code 42, got %d", result.ExitCode)
	}
	if result.Err == nil {
		t.Fatal("expected non-nil error for failed command")
	}
}

func TestRunCapturesStderrEvents(t *testing.T) {
	result := exec.Run(context.Background(), exec.Command{
		Name: "sh",
		Args: []string{"-c", "echo err-line >&2"},
	}, nil)

	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", result.ExitCode)
	}
	stderr := filterEvents(result.Events, exec.EventStderr)
	if len(stderr) == 0 {
		t.Fatal("expected at least one stderr event")
	}
	if stderr[0].Data != "err-line" {
		t.Errorf("expected 'err-line', got %q", stderr[0].Data)
	}
}

func TestRunTimeoutKillsProcess(t *testing.T) {
	result := exec.Run(context.Background(), exec.Command{
		Name:    "sleep",
		Args:    []string{"10"},
		Timeout: 50 * time.Millisecond,
	}, nil)

	if result.Err == nil {
		t.Fatal("expected error from timeout")
	}
	if !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", result.Err)
	}
}

func TestRunContextCancellationStopsProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	result := exec.Run(ctx, exec.Command{
		Name: "sleep",
		Args: []string{"10"},
	}, nil)

	if result.Err == nil {
		t.Fatal("expected error from cancellation")
	}
	if !errors.Is(result.Err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", result.Err)
	}
}

func TestRunStreamsEventsToHandler(t *testing.T) {
	var streamed []exec.Event
	handler := func(e exec.Event) {
		streamed = append(streamed, e)
	}

	result := exec.Run(context.Background(), exec.Command{
		Name: "sh",
		Args: []string{"-c", "echo line1; echo line2 >&2; echo line3"},
	}, handler)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if len(streamed) != 3 {
		t.Fatalf("expected 3 streamed events, got %d", len(streamed))
	}
	// Events in Result should match what was streamed.
	if len(result.Events) != len(streamed) {
		t.Errorf("result events (%d) != streamed events (%d)", len(result.Events), len(streamed))
	}
}

func TestRunDeliversStdinToProcess(t *testing.T) {
	result := exec.Run(context.Background(), exec.Command{
		Name:  "cat",
		Stdin: "hello from stdin",
	}, nil)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	stdout := filterEvents(result.Events, exec.EventStdout)
	if len(stdout) == 0 {
		t.Fatal("expected stdout from cat")
	}
	if stdout[0].Data != "hello from stdin" {
		t.Errorf("expected %q, got %q", "hello from stdin", stdout[0].Data)
	}
}

func TestRunEmptyStdinDoesNotHang(t *testing.T) {
	result := exec.Run(context.Background(), exec.Command{
		Name: "cat",
	}, nil)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	// cat with no stdin reads EOF immediately and produces no output
	stdout := filterEvents(result.Events, exec.EventStdout)
	if len(stdout) != 0 {
		t.Errorf("expected no stdout for cat with empty stdin, got %v", stdout)
	}
}

func TestRunMergesCommandEnvOverOsEnviron(t *testing.T) {
	t.Setenv("SPRINGFIELD_TEST_INHERITED", "from_parent")
	result := exec.Run(context.Background(), exec.Command{
		Name: "sh",
		Args: []string{"-c", "echo $SPRINGFIELD_TEST_INHERITED $SPRINGFIELD_TEST_OVERRIDE"},
		Env: map[string]string{
			"SPRINGFIELD_TEST_OVERRIDE": "from_cmd",
		},
	}, nil)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	stdout := filterEvents(result.Events, exec.EventStdout)
	if len(stdout) == 0 {
		t.Fatal("expected stdout")
	}
	if stdout[0].Data != "from_parent from_cmd" {
		t.Fatalf("env merge: want %q, got %q", "from_parent from_cmd", stdout[0].Data)
	}
}

func TestRunCommandEnvOverridesParent(t *testing.T) {
	t.Setenv("SPRINGFIELD_TEST_KEY", "parent_val")
	result := exec.Run(context.Background(), exec.Command{
		Name: "sh",
		Args: []string{"-c", "echo $SPRINGFIELD_TEST_KEY"},
		Env: map[string]string{
			"SPRINGFIELD_TEST_KEY": "override_val",
		},
	}, nil)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	stdout := filterEvents(result.Events, exec.EventStdout)
	if len(stdout) == 0 || stdout[0].Data != "override_val" {
		t.Fatalf("env override: want override_val, got %v", stdout)
	}
}

// TestRunCapturesLargeStdoutLine is a regression for the bug where
// claude-code stream-json events wrapping a tool_result for a Read of a
// moderately large file (~50 KB) exceeded bufio.Scanner's default 64 KiB
// per-line cap, silently terminating Springfield's stdout reader and losing
// every subsequent event. Lines well above 64 KiB must round-trip intact.
func TestRunCapturesLargeStdoutLine(t *testing.T) {
	const lineLen = 200 * 1024 // 200 KiB — comfortably past the 64 KiB default
	script := fmt.Sprintf(`head -c %d < /dev/zero | tr '\0' 'x'; echo`, lineLen)

	result := exec.Run(context.Background(), exec.Command{
		Name: "sh",
		Args: []string{"-c", script},
	}, nil)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	stdout := filterEvents(result.Events, exec.EventStdout)
	if len(stdout) != 1 {
		t.Fatalf("expected exactly one stdout event, got %d", len(stdout))
	}
	if got := len(stdout[0].Data); got != lineLen {
		t.Fatalf("captured line length = %d, want %d (scanner likely hit default 64 KiB cap)", got, lineLen)
	}
	if strings.Count(stdout[0].Data, "x") != lineLen {
		t.Fatalf("captured content corrupted: not all 'x' characters")
	}
}

// TestRunCapturesLargeStderrLine — same regression on the stderr scanner.
func TestRunCapturesLargeStderrLine(t *testing.T) {
	const lineLen = 200 * 1024
	script := fmt.Sprintf(`head -c %d < /dev/zero | tr '\0' 'x' >&2; echo >&2`, lineLen)

	result := exec.Run(context.Background(), exec.Command{
		Name: "sh",
		Args: []string{"-c", script},
	}, nil)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	stderr := filterEvents(result.Events, exec.EventStderr)
	if len(stderr) != 1 {
		t.Fatalf("expected exactly one stderr event, got %d", len(stderr))
	}
	if got := len(stderr[0].Data); got != lineLen {
		t.Fatalf("captured line length = %d, want %d", got, lineLen)
	}
}

func filterEvents(events []exec.Event, typ exec.EventType) []exec.Event {
	var filtered []exec.Event
	for _, e := range events {
		if e.Type == typ {
			filtered = append(filtered, e)
		}
	}
	return filtered
}
