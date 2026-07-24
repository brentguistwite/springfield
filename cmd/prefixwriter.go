package cmd

import (
	"bytes"
	"io"
	"sync"
)

// syncLineWriter serializes whole-line writes from concurrent plan streams so
// interleaved output stays line-atomic. All prefixWriters of one batch share
// a single instance.
type syncLineWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncLineWriter) writeLine(line []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.w.Write(line)
}

// prefixWriter buffers a single plan's progress stream and emits complete
// lines prefixed with the plan ID through the shared syncLineWriter. The
// prefix is applied for the writer's whole lifetime (phase-static — the
// caller decides once per phase, never mid-stream). Not safe for concurrent
// use by multiple goroutines; each plan goroutine owns its own prefixWriter.
type prefixWriter struct {
	out    *syncLineWriter
	prefix []byte
	buf    []byte
}

func newPrefixWriter(out *syncLineWriter, planID string) *prefixWriter {
	return &prefixWriter{out: out, prefix: []byte("[" + planID + "] ")}
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	for {
		idx := bytes.IndexByte(p.buf, '\n')
		if idx < 0 {
			return len(b), nil
		}
		line := make([]byte, 0, len(p.prefix)+idx+1)
		line = append(line, p.prefix...)
		line = append(line, p.buf[:idx+1]...)
		p.out.writeLine(line)
		p.buf = p.buf[idx+1:]
	}
}

// Flush emits any trailing partial line (newline-terminated so the prefix
// invariant holds for the next writer). Call once when the plan settles.
func (p *prefixWriter) Flush() {
	if len(p.buf) == 0 {
		return
	}
	line := make([]byte, 0, len(p.prefix)+len(p.buf)+1)
	line = append(line, p.prefix...)
	line = append(line, p.buf...)
	line = append(line, '\n')
	p.out.writeLine(line)
	p.buf = nil
}
