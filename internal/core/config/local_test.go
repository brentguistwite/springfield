package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReviewConfigMaxReviewIterationsOrDefault(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"unset defaults to 3", 0, 3},
		{"negative defaults to 3", -2, 3},
		{"explicit positive kept", 5, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ReviewConfig{MaxReviewIterations: tc.in}
			if got := r.MaxReviewIterationsOrDefault(); got != tc.want {
				t.Fatalf("MaxReviewIterationsOrDefault() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDefaultMaxReviewIterationsIsThree(t *testing.T) {
	if DefaultMaxReviewIterations != 3 {
		t.Fatalf("DefaultMaxReviewIterations = %d, want 3", DefaultMaxReviewIterations)
	}
}

func TestLoadLocalFromMissingFileReturnsZeroDisabled(t *testing.T) {
	dir := t.TempDir() // no springfield.local.toml present
	lc, err := LoadLocalFrom(dir)
	if err != nil {
		t.Fatalf("LoadLocalFrom on missing file: unexpected error %v", err)
	}
	if lc.Review.Enabled {
		t.Fatalf("missing file should leave review disabled, got Enabled=true")
	}
}

func TestLoadLocalFromParsesReviewBlock(t *testing.T) {
	dir := t.TempDir()
	body := `
[review]
enabled = true
agent = "codex"
prompt = "Run the adversarial-review skill on this diff."
max_review_iterations = 2
`
	if err := os.WriteFile(filepath.Join(dir, LocalFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	lc, err := LoadLocalFrom(dir)
	if err != nil {
		t.Fatalf("LoadLocalFrom: %v", err)
	}
	if !lc.Review.Enabled || lc.Review.Agent != "codex" ||
		lc.Review.Prompt == "" || lc.Review.MaxReviewIterations != 2 {
		t.Fatalf("parsed review block mismatch: %+v", lc.Review)
	}
}

func TestLoadLocalFromMalformedReturnsInvalidConfigError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, LocalFileName), []byte("[review\nenabled = true"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadLocalFrom(dir)
	if err == nil {
		t.Fatal("expected error on malformed toml, got nil")
	}
	var invalid *InvalidConfigError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected *InvalidConfigError, got %T", err)
	}
}
