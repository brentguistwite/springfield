package cmd_test

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestHookGuardAcceptsGeminiToolInputShapes pipes verbatim Gemini payloads
// into the hook-guard subcommand. Locks the invariant that Gemini's
// tool_input field names (file_path, command) match the existing Claude
// schema we guard against. If Gemini introduces new keys (e.g. "path"),
// extend hookGuardShouldBlock — test-first.
func TestHookGuardAcceptsGeminiToolInputShapes(t *testing.T) {
	bin := buildBinary(t)

	cases := []struct {
		name     string
		stdin    string
		wantExit int
		wantErr  string
	}{
		{
			name:     "gemini write_file to springfield control plane",
			stdin:    `{"tool_name":"write_file","tool_input":{"file_path":".springfield/batch.json","content":"..."}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "gemini replace on springfield file",
			stdin:    `{"tool_name":"replace","tool_input":{"file_path":".springfield/batch.json","old_string":"x","new_string":"y"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "gemini run_shell_command springfield recursion",
			stdin:    `{"tool_name":"run_shell_command","tool_input":{"command":"springfield start"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "gemini run_shell_command touching control plane",
			stdin:    `{"tool_name":"run_shell_command","tool_input":{"command":"rm -rf .springfield/"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "gemini read_file README allowed",
			stdin:    `{"tool_name":"read_file","tool_input":{"file_path":"README.md"}}`,
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
				t.Fatalf("exit = %d, want %d (stderr=%q)", exit, tc.wantExit, stderr.String())
			}
			if tc.wantErr != "" && !strings.Contains(stderr.String(), tc.wantErr) {
				t.Fatalf("stderr missing %q: got %q", tc.wantErr, stderr.String())
			}
		})
	}
}
