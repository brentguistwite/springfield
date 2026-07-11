package planrun

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestPromptTailKeepsValidUTF8 proves the verify-fix prompt's front-truncation
// advances to a rune boundary: a stream of multi-byte runes cut at an arbitrary
// byte offset must never embed a partial leading rune (invalid UTF-8) in the
// prompt handed to the fix agent.
func TestPromptTailKeepsValidUTF8(t *testing.T) {
	// "世" is 3 bytes; 8KiB is not divisible by 3, so the byte cut at
	// verifyPromptTailBytes is guaranteed to split a rune.
	big := strings.Repeat("世", 8*1024) // 24KiB, over the 8KiB prompt tail
	got := promptTail(big)
	if len(got) >= len(big) {
		t.Fatalf("promptTail did not truncate: len=%d input=%d", len(got), len(big))
	}
	if !utf8.ValidString(got) {
		t.Fatal("promptTail output contains invalid UTF-8 — the byte cut split a rune")
	}
	if !strings.Contains(got, "truncated") {
		t.Fatal("promptTail dropped its truncation notice")
	}
}
