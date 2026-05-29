package planrun

import (
	"testing"
	"unicode/utf8"
)

// TestTruncateForErrorRuneAware pins that truncation is rune-based, not
// byte-based, so multi-byte UTF-8 reviewer output (em-dashes, smart quotes,
// non-ASCII identifiers, emoji) is never sliced mid-rune. Mid-rune slices
// produce invalid UTF-8 that gets persisted into .springfield state files
// and surfaced in `springfield status`.
func TestTruncateForErrorRuneAware(t *testing.T) {
	// 10 em-dashes (3 bytes each = 30 bytes; 10 runes).
	const dash = "—"
	input := ""
	for i := 0; i < 10; i++ {
		input += dash
	}

	// Truncate to 5 runes. Byte-based truncation at 5 would slice mid-rune
	// (after the 2nd byte of the 2nd em-dash) and produce invalid UTF-8.
	got := truncateForError(input, 5)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated output must be valid UTF-8, got %q (bytes: %x)", got, []byte(got))
	}
	// Expect 5 em-dashes plus the ellipsis (1 rune, 3 bytes).
	wantRunes := 5 + 1 // 5 dashes + 1 ellipsis
	if rc := utf8.RuneCountInString(got); rc != wantRunes {
		t.Fatalf("truncated rune count = %d, want %d (output: %q)", rc, wantRunes, got)
	}
}

// TestTruncateForErrorAsciiBoundary keeps the existing ASCII contract:
// max-byte == max-rune for pure ASCII; ellipsis only when truncation happened.
func TestTruncateForErrorAsciiBoundary(t *testing.T) {
	if got := truncateForError("abcdefghij", 5); got != "abcde…" {
		t.Fatalf("ASCII truncation: got %q, want %q", got, "abcde…")
	}
	if got := truncateForError("abcde", 5); got != "abcde" {
		t.Fatalf("no truncation when len == max: got %q, want %q", got, "abcde")
	}
	if got := truncateForError("abc", 5); got != "abc" {
		t.Fatalf("no truncation when len < max: got %q, want %q", got, "abc")
	}
}

// TestTruncateForErrorCollapsesWhitespace pins the secondary contract:
// internal whitespace runs are collapsed so the excerpt fits on one line.
func TestTruncateForErrorCollapsesWhitespace(t *testing.T) {
	got := truncateForError("alpha\n\nbeta\t  gamma", 100)
	if want := "alpha beta gamma"; got != want {
		t.Fatalf("whitespace collapse: got %q, want %q", got, want)
	}
}
