package exec

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

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

	var (
		events []Event
		mu     sync.Mutex
	)
	emit := func(e Event) {
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
		for scanner.Scan() {
			emit(Event{Type: EventStdout, Data: scanner.Text(), Time: time.Now()})
		}
	}()
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			emit(Event{Type: EventStderr, Data: scanner.Text(), Time: time.Now()})
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
