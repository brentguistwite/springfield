// Command capture-fixture records a REAL agent-CLI transcript into the
// tests/realcaptures corpus so transport parsers are tested against bytes the
// production CLI actually emits — never hand-authored JSON.
//
// It runs the canonical production invocation for a tool (the same stream-json
// flags the adapters use), writes stdout verbatim to
// tests/realcaptures/<tool>/<scenario>.jsonl, and writes a sibling
// <scenario>.meta.json recording {tool, tool_version, command_args,
// captured_at, sha256}. The sha256 is over the .jsonl bytes; the integrity
// gate (TestFixturesVerbatimAndWellFormed) recomputes it to catch hand-edits.
//
// Authenticity is NOT machine-proven — a determined contributor could fabricate
// a .jsonl and a matching meta. The corpus's value is producer-SHAPE fidelity +
// immutability-after-commit; real origin is delegated to tool_version + review.
//
// Usage:
//
//	go run ./cmd/capture-fixture -tool claude -scenario reviewer-verdict-pass \
//	    -model claude-sonnet-4-6 -prompt 'Reply with exactly: <review-verdict>pass</review-verdict>'
//
// Prompt may also be piped on stdin (omit -prompt). Run from the repo root.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "capture-fixture:", err)
		os.Exit(1)
	}
}

type meta struct {
	Tool        string   `json:"tool"`
	ToolVersion string   `json:"tool_version"`
	Scenario    string   `json:"scenario"`
	CommandArgs []string `json:"command_args"`
	CapturedAt  string   `json:"captured_at"`
	SHA256      string   `json:"sha256"`
	Notes       string   `json:"notes,omitempty"`
}

func run() error {
	tool := flag.String("tool", "", "agent CLI to capture: claude | codex")
	scenario := flag.String("scenario", "", "fixture name, e.g. reviewer-verdict-pass-no-tools")
	model := flag.String("model", "", "optional model override")
	prompt := flag.String("prompt", "", "prompt text (or pipe on stdin)")
	outDir := flag.String("outdir", "tests/realcaptures", "corpus root")
	notes := flag.String("notes", "", "optional note recorded in meta")
	flag.Parse()

	if *tool == "" || *scenario == "" {
		return fmt.Errorf("-tool and -scenario are required")
	}
	text := *prompt
	if text == "" {
		piped, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin prompt: %w", err)
		}
		text = strings.TrimSpace(string(piped))
	}
	if text == "" {
		return fmt.Errorf("empty prompt (pass -prompt or pipe on stdin)")
	}

	bin, args, stdin := buildInvocation(*tool, *model, text)
	if bin == "" {
		return fmt.Errorf("unknown -tool %q (want claude|codex)", *tool)
	}

	version := toolVersion(bin)

	cmd := exec.Command(bin, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	fmt.Fprintf(os.Stderr, "running: %s %s\n", bin, strings.Join(args, " "))
	if err := cmd.Run(); err != nil {
		// A non-zero exit can still produce a useful transcript (e.g. an error
		// scenario), so warn but keep going if we captured any bytes.
		fmt.Fprintf(os.Stderr, "warning: %s exited with error: %v\n", bin, err)
		if stdout.Len() == 0 {
			return fmt.Errorf("%s produced no stdout to capture", bin)
		}
	}

	raw := stdout.Bytes()
	sum := sha256.Sum256(raw)
	dir := filepath.Join(*outDir, *tool)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	jsonlPath := filepath.Join(dir, *scenario+".jsonl")
	if err := os.WriteFile(jsonlPath, raw, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", jsonlPath, err)
	}

	m := meta{
		Tool:        *tool,
		ToolVersion: version,
		Scenario:    *scenario,
		CommandArgs: args,
		CapturedAt:  time.Now().UTC().Format(time.RFC3339),
		SHA256:      fmt.Sprintf("%x", sum),
		Notes:       *notes,
	}
	mj, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	metaPath := filepath.Join(dir, *scenario+".meta.json")
	if err := os.WriteFile(metaPath, append(mj, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", metaPath, err)
	}

	fmt.Fprintf(os.Stderr, "captured %d bytes -> %s\n%s\n", len(raw), jsonlPath, metaPath)
	return nil
}

// buildInvocation returns the canonical production-shaped command per tool.
// Returns (binary, args, stdin). Mirrors the adapter Command() builders so the
// captured stdout matches what Springfield parses in production.
func buildInvocation(tool, model, prompt string) (string, []string, string) {
	switch tool {
	case "claude":
		// Mirrors claude adapter: -p --output-format stream-json --verbose,
		// prompt via stdin. (Control-plane --settings/--permission-mode are
		// omitted; they don't affect the stdout event shape for a benign run.)
		args := []string{"-p", "--output-format", "stream-json", "--verbose"}
		if model != "" {
			args = append(args, "--model", model)
		}
		return "claude", args, prompt
	case "codex":
		// Mirrors codex adapter: exec --json, prompt as positional arg, plus
		// the autonomous bypass + skip-git-repo-check so a capture never hangs.
		args := []string{"exec", "--json", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox"}
		if model != "" {
			args = append(args, "--model", model)
		}
		args = append(args, prompt)
		return "codex", args, ""
	default:
		return "", nil, ""
	}
}

func toolVersion(bin string) string {
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
