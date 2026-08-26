// Package fixtures loads captured agent-CLI transcripts into the exact
// coreexec.Event stream the production stdout reader produces, so transport
// parsers (verdict scan, marker scan, num_turns, ValidateResult) are tested
// against producer-shaped bytes — never a hand-authored convenience struct.
//
// The whole point: a captured `claude -p --output-format stream-json` line is
// one JSON object whose embedded newlines are the literal 2-byte `\n` escape.
// Loading it verbatim into Event.Data forces a test's input to match what the
// real CLI emits at the boundary, which is where the dogfood gate bugs hid.
package fixtures

import (
	"bufio"
	"os"
	"testing"

	coreexec "springfield/internal/core/exec"
)

// LoadEvents reads a captured transcript (one JSON object per line, verbatim
// CLI stdout) and yields one EventStdout per line — byte-for-byte the same
// split the production reader performs in internal/core/exec (bufio.Scanner
// over stdout, see exec index.go: Buffer max 16 MiB, Data = scanner.Text()).
// No decoding, no blank-line filtering: the bytes a test sees are the bytes
// the parser sees in production.
func LoadEvents(t testing.TB, path string) []coreexec.Event {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	var events []coreexec.Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		events = append(events, coreexec.Event{Type: coreexec.EventStdout, Data: scanner.Text()})
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan fixture %s: %v", path, err)
	}
	return events
}
