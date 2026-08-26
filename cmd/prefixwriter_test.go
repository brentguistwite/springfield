package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// TestPrefixWriterKeepsLinesAtomicAndPrefixedUnderConcurrency verifies the
// two properties concurrent plan output depends on: every emitted line
// carries its own plan's prefix for the writer's whole lifetime, and lines
// from concurrent writers never interleave mid-line.
func TestPrefixWriterKeepsLinesAtomicAndPrefixedUnderConcurrency(t *testing.T) {
	var sink bytes.Buffer
	shared := &syncLineWriter{w: &sink}

	const lines = 50
	var wg sync.WaitGroup
	for _, plan := range []string{"alpha", "beta"} {
		wg.Add(1)
		go func(plan string) {
			defer wg.Done()
			pw := newPrefixWriter(shared, plan)
			defer pw.Flush()
			for i := 0; i < lines; i++ {
				// Split one logical line across two writes to exercise buffering.
				_, _ = fmt.Fprintf(pw, "%s line %d", plan, i)
				_, _ = fmt.Fprintf(pw, " end\n")
			}
		}(plan)
	}
	wg.Wait()

	got := strings.Split(strings.TrimSuffix(sink.String(), "\n"), "\n")
	if len(got) != 2*lines {
		t.Fatalf("emitted %d lines, want %d", len(got), 2*lines)
	}
	counts := map[string]int{}
	for _, line := range got {
		switch {
		case strings.HasPrefix(line, "[alpha] alpha line ") && strings.HasSuffix(line, " end"):
			counts["alpha"]++
		case strings.HasPrefix(line, "[beta] beta line ") && strings.HasSuffix(line, " end"):
			counts["beta"]++
		default:
			t.Fatalf("malformed or mis-prefixed line: %q", line)
		}
	}
	if counts["alpha"] != lines || counts["beta"] != lines {
		t.Fatalf("line counts = %v, want %d each", counts, lines)
	}
}

// TestPrefixWriterFlushEmitsTrailingPartialLine covers the settle-time flush
// of an unterminated final line.
func TestPrefixWriterFlushEmitsTrailingPartialLine(t *testing.T) {
	var sink bytes.Buffer
	pw := newPrefixWriter(&syncLineWriter{w: &sink}, "p1")
	_, _ = fmt.Fprintf(pw, "no newline yet")
	if sink.Len() != 0 {
		t.Fatalf("partial line emitted before flush: %q", sink.String())
	}
	pw.Flush()
	if got := sink.String(); got != "[p1] no newline yet\n" {
		t.Fatalf("flushed output = %q", got)
	}
	pw.Flush() // idempotent
	if got := sink.String(); got != "[p1] no newline yet\n" {
		t.Fatalf("second flush changed output: %q", got)
	}
}
