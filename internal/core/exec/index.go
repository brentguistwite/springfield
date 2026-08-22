package exec

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// maxScannerLine caps a single line read from a subprocess's stdout or
// stderr. The default bufio.Scanner cap is 64 KiB, which is too small for
// stream-json events emitted by claude-code's --output-format stream-json:
// a tool_result wrapping a Read of any moderately large file (a ~50 KB Go
// source file already blows past it) silently terminates the scanner AND
// blocks the subprocess once the OS pipe buffer fills, deadlocking the
// run. 16 MiB fits any plausible tool_result while leaving a memory cap
// against a runaway process.
const maxScannerLine = 16 * 1024 * 1024

// Run executes a subprocess, streams output via handler, and returns
// a structured result. If cmd.Timeout > 0, the context is wrapped
// with a deadline. Events are collected in Result.Events regardless
// of whether a handler is provided.
func Run(ctx context.Context, cmd Command, handler EventHandler) Result {
	if cmd.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cmd.Timeout)
		defer cancel()
	}

	proc := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	if cmd.Stdin != "" {
		proc.Stdin = strings.NewReader(cmd.Stdin)
	}
	if cmd.Dir != "" {
		proc.Dir = cmd.Dir
	}
	if len(cmd.Env) > 0 {
		proc.Env = mergeEnv(os.Environ(), cmd.Env)
	}

	stdout, err := proc.StdoutPipe()
	if err != nil {
		return Result{ExitCode: -1, Err: err}
	}
	stderr, err := proc.StderrPipe()
	if err != nil {
		return Result{ExitCode: -1, Err: err}
	}

	if err := proc.Start(); err != nil {
		return Result{ExitCode: -1, Err: err}
	}

	// Stall detection races the wall-clock deadline: the watcher runs for the
	// subprocess's lifetime and is stopped the moment Run returns (after
	// proc.Wait below). It only observes — it never signals or kills the proc.
	// Watch invokes its OnStall callback synchronously, so cancelling is not
	// enough: we JOIN the goroutine before returning, guaranteeing no in-flight
	// callback outlives the dispatch (otherwise a late escalation could mutate
	// plan state after the caller has already written the terminal outcome).
	if cmd.Stall != nil {
		watchCtx, stopWatch := context.WithCancel(ctx)
		watchDone := make(chan struct{})
		go func() { defer close(watchDone); cmd.Stall.Watch(watchCtx) }()
		defer func() { stopWatch(); <-watchDone }()
	}

	var (
		events []Event
		mu     sync.Mutex
	)
	emit := func(e Event) {
		// Heartbeat the stall monitor at the live consumption point: every
		// stream event resets its idle timer. Kept outside the events mutex —
		// the monitor guards its own state.
		if cmd.Stall != nil {
			cmd.Stall.Observe()
		}
		mu.Lock()
		events = append(events, e)
		if handler != nil {
			handler(e)
		}
		mu.Unlock()
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), maxScannerLine)
		for scanner.Scan() {
			emit(Event{Type: EventStdout, Data: scanner.Text(), Time: time.Now()})
		}
		// A silent scanner exit on ErrTooLong has historically swallowed
		// every event after the offending line; surface the failure so
		// callers (and operators reading evidence) see it. Emitted on
		// stderr so existing classifiers can act on it.
		if err := scanner.Err(); err != nil {
			emit(Event{
				Type: EventStderr,
				Data: fmt.Sprintf("springfield: stdout scanner error: %v", err),
				Time: time.Now(),
			})
		}
	}()
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 64*1024), maxScannerLine)
		for scanner.Scan() {
			emit(Event{Type: EventStderr, Data: scanner.Text(), Time: time.Now()})
		}
		if err := scanner.Err(); err != nil {
			emit(Event{
				Type: EventStderr,
				Data: fmt.Sprintf("springfield: stderr scanner error: %v", err),
				Time: time.Now(),
			})
		}
	}()

	wg.Wait()

	waitErr := proc.Wait()
	exitCode := proc.ProcessState.ExitCode()

	// If the context caused the kill, surface that as the error.
	if ctx.Err() != nil {
		waitErr = ctx.Err()
	}

	return Result{ExitCode: exitCode, Events: events, Err: waitErr}
}

// mergeEnv produces a key=value slice from base plus overrides, with
// overrides winning on duplicate keys. Used so adapters can inject a small
// number of environment overrides without clobbering the parent env.
func mergeEnv(base []string, overrides map[string]string) []string {
	seen := make(map[string]bool, len(overrides))
	merged := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		eq := strings.IndexByte(entry, '=')
		if eq < 0 {
			merged = append(merged, entry)
			continue
		}
		key := entry[:eq]
		if override, ok := overrides[key]; ok {
			merged = append(merged, key+"="+override)
			seen[key] = true
			continue
		}
		merged = append(merged, entry)
	}
	for k, v := range overrides {
		if seen[k] {
			continue
		}
		merged = append(merged, k+"="+v)
	}
	return merged
}
