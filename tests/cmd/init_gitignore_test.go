package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCreatesGitignoreWhenMissing(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude", "--tracked-gitignore")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, output)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, ".springfield/*") {
		t.Errorf("expected .springfield/* in .gitignore, got:\n%s", content)
	}
	if !strings.Contains(content, "!.springfield/plans/") {
		t.Errorf("expected plans un-ignore in .gitignore, got:\n%s", content)
	}
	if !strings.Contains(content, ".springfield/plans/*/") {
		t.Errorf("expected batch runtime ignore in .gitignore, got:\n%s", content)
	}
	if !strings.Contains(output, "Added Springfield patterns to .gitignore") {
		t.Errorf("expected Added message in stdout, got:\n%s", output)
	}
}

func TestInitAppendsToExistingGitignore(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	existing := "node_modules/\ndist/\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude", "--tracked-gitignore"); err != nil {
		t.Fatalf("init: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	content := string(data)
	if !strings.Contains(content, "node_modules/") || !strings.Contains(content, "dist/") {
		t.Errorf("existing entries dropped, got:\n%s", content)
	}
	if !strings.Contains(content, ".springfield/*") {
		t.Errorf("expected .springfield/* appended, got:\n%s", content)
	}
}

func TestInitGitignoreIsIdempotent(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude", "--tracked-gitignore"); err != nil {
		t.Fatalf("first init: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude", "--tracked-gitignore")
	if err != nil {
		t.Fatalf("second init: %v", err)
	}
	if strings.Contains(output, "Added Springfield patterns to .gitignore") {
		t.Errorf("second init should NOT announce gitignore add, got:\n%s", output)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if got := strings.Count(string(data), ".springfield/*"); got != 1 {
		t.Errorf("expected exactly 1 .springfield/* entry, got %d:\n%s", got, string(data))
	}
}

func TestInitGitignoreReplacesOldBlanketIgnore(t *testing.T) {
	cases := []string{
		".springfield",
		".springfield/",
		"/.springfield",
		"/.springfield/",
	}
	for _, existing := range cases {
		t.Run(existing, func(t *testing.T) {
			bin := buildBinary(t)
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(existing+"\n"), 0o644); err != nil {
				t.Fatalf("seed .gitignore: %v", err)
			}

			output, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude", "--tracked-gitignore")
			if err != nil {
				t.Fatalf("init: %v", err)
			}
			if !strings.Contains(output, "Added Springfield patterns to .gitignore") {
				t.Errorf("init should replace old blanket pattern %q with new block, got:\n%s", existing, output)
			}
			data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
			content := string(data)
			if !strings.Contains(content, ".springfield/*") {
				t.Errorf("new block missing, got:\n%s", content)
			}
			if !strings.Contains(content, "!.springfield/plans/") {
				t.Errorf("plans un-ignore missing, got:\n%s", content)
			}
		})
	}
}

func TestInitGitignoreRecognizesNewBlockAsIdempotent(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	block := ".springfield/*\n!.springfield/plans/\n.springfield/plans/*/\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(block), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude", "--tracked-gitignore")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if strings.Contains(output, "Added Springfield patterns to .gitignore") {
		t.Errorf("should recognize existing new-style block as idempotent, got:\n%s", output)
	}
}
