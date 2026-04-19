package agents_test

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"springfield/internal/core/agents/claude"
)

// TestClaudeControlPlaneHookScript runs the literal hook command string
// under bash with representative tool-input payloads on stdin and asserts
// exit code + stderr. This pins the shell-level behavior the adapter relies
// on: catching both lexical (relative) and bypass (absolute path, cd,
// redirects) forms that target .springfield/.
func TestClaudeControlPlaneHookScript(t *testing.T) {
	script := claude.SpringfieldControlPlaneHookCommand()

	cases := []struct {
		name     string
		stdin    string
		wantExit int
		wantErr  string // substring that must appear (or must NOT if wantExit==0)
	}{
		{
			name:     "relative path write",
			stdin:    `{"tool_input":{"file_path":".springfield/batch.json"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "absolute path write",
			stdin:    `{"tool_input":{"file_path":"/abs/path/.springfield/batch.json"}}`,
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
			name:     "bash redirect into control plane",
			stdin:    `{"tool_input":{"command":"echo x > .springfield/run.json"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
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
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", script)
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
