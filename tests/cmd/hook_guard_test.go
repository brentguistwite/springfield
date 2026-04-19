package cmd_test

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestHookGuardPathAwareness verifies the hidden `springfield hook-guard`
// subcommand only blocks tool calls when a path-bearing field of the
// tool_input JSON contains `.springfield`. Content/diff bodies that merely
// mention .springfield in prose must NOT be blocked.
func TestHookGuardPathAwareness(t *testing.T) {
	bin := buildBinary(t)

	cases := []struct {
		name     string
		stdin    string
		wantExit int
		wantErr  string // substring that must appear (or must NOT when wantExit==0)
	}{
		{
			name:     "relative file_path",
			stdin:    `{"tool_input":{"file_path":".springfield/batch.json"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "absolute file_path",
			stdin:    `{"tool_input":{"file_path":"/abs/path/.springfield/batch.json"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "notebook_path",
			stdin:    `{"tool_input":{"notebook_path":".springfield/notebook.ipynb"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "bash cd then rm",
			stdin:    `{"tool_input":{"command":"cd .springfield && rm run.json"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "bash redirect",
			stdin:    `{"tool_input":{"command":"echo x > .springfield/run.json"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "multiedit edits array",
			stdin:    `{"tool_input":{"edits":[{"file_path":".springfield/batch.json"}]}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "path name literally contains .springfield",
			stdin:    `{"tool_input":{"file_path":"docs/why-we-avoid-.springfield.md"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "file_path clean + content mentions .springfield",
			stdin:    `{"tool_input":{"file_path":"src/main.go","content":"// see .springfield dir"}}`,
			wantExit: 0,
		},
		{
			name:     "unrelated file allowed",
			stdin:    `{"tool_input":{"file_path":"src/main.go"}}`,
			wantExit: 0,
		},
		{
			name:     "unrelated bash command allowed",
			stdin:    `{"tool_input":{"command":"grep foo src/"}}`,
			wantExit: 0,
		},
		{
			name:     "malformed json fails open",
			stdin:    `not json`,
			wantExit: 0,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, "hook-guard")
			cmd.Stdin = strings.NewReader(tc.stdin)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err := cmd.Run()
			exit := 0
			if err != nil {
				ee, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("run: %v (stderr=%q)", err, stderr.String())
				}
				exit = ee.ExitCode()
			}

			if exit != tc.wantExit {
				t.Fatalf("exit = %d, want %d (stderr=%q stdout=%q)", exit, tc.wantExit, stderr.String(), stdout.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}

			stderrStr := stderr.String()
			if tc.wantErr != "" {
				if !strings.Contains(stderrStr, tc.wantErr) {
					t.Fatalf("stderr = %q, want substring %q", stderrStr, tc.wantErr)
				}
			} else {
				if strings.Contains(stderrStr, "off-limits") {
					t.Fatalf("expected no off-limits mention, got stderr=%q", stderrStr)
				}
			}
		})
	}
}

// TestHookGuardIsHiddenFromHelp ensures the subcommand exists but is not
// listed in the primary help output.
func TestHookGuardIsHiddenFromHelp(t *testing.T) {
	bin := buildBinary(t)
	output, err := runBinaryIn(t, bin, t.TempDir(), "--help")
	if err != nil {
		t.Fatalf("help: %v\n%s", err, output)
	}
	if strings.Contains(output, "hook-guard") {
		t.Fatalf("hook-guard should be hidden from help, got:\n%s", output)
	}
	// But must be invokable.
	output2, err := runBinaryIn(t, bin, t.TempDir(), "hook-guard", "--help")
	if err != nil {
		t.Fatalf("hook-guard --help: %v\n%s", err, output2)
	}
}
