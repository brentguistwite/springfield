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
		// --- command-field mutation awareness ---
		// A Bash command that merely MENTIONS .springfield (commit body, grep,
		// read) must pass; only one that MUTATES a .springfield path blocks.
		{
			name:     "commit message body (read-only prose) mentions .springfield",
			stdin:    `{"tool_input":{"command":"git commit -F - <<'EOF'\nfix: never write .springfield/execution/config.json\nEOF"}}`,
			wantExit: 0,
		},
		{
			// Documents the accepted residual: with no shell parser, a mutation
			// verb at a line-start inside a heredoc body still trips the guard.
			// Route such bodies through a file (-F path) rather than inline.
			name:     "heredoc body with mutation verb at line start still blocks (residual)",
			stdin:    `{"tool_input":{"command":"git commit -F - <<'EOF'\nrm the stale .springfield/run.json handling\nEOF"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "redirect noclobber-override into control plane blocked",
			stdin:    `{"tool_input":{"command":"echo '{}' >| .springfield/execution/config.json"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "redirect both-streams into control plane blocked",
			stdin:    `{"tool_input":{"command":"run >& .springfield/log"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "find -delete on control plane blocked",
			stdin:    `{"tool_input":{"command":"find .springfield -name '*.json' -delete"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "read-only find on control plane allowed",
			stdin:    `{"tool_input":{"command":"find .springfield -name '*.json'"}}`,
			wantExit: 0,
		},
		{
			name:     "fd dup redirect not mistaken for control-plane write",
			stdin:    `{"tool_input":{"command":"echo .springfield 2>&1 | cat"}}`,
			wantExit: 0,
		},
		{
			name:     "grep of .springfield in source allowed",
			stdin:    `{"tool_input":{"command":"rg '.springfield' cmd/hook_guard.go"}}`,
			wantExit: 0,
		},
		{
			name:     "reading a control-plane file allowed",
			stdin:    `{"tool_input":{"command":"cat .springfield/execution/config.json"}}`,
			wantExit: 0,
		},
		{
			name:     "ls of control plane allowed",
			stdin:    `{"tool_input":{"command":"ls -la .springfield/"}}`,
			wantExit: 0,
		},
		{
			name:     "echo of .springfield text to unrelated file allowed",
			stdin:    `{"tool_input":{"command":"echo '.springfield is internal' > /tmp/notes.txt"}}`,
			wantExit: 0,
		},
		{
			name:     "rm targeting control plane blocked",
			stdin:    `{"tool_input":{"command":"rm -rf .springfield/run.json"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "append redirect into control plane blocked",
			stdin:    `{"tool_input":{"command":"echo x >> .springfield/state.json"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "tee into control plane blocked",
			stdin:    `{"tool_input":{"command":"printf '{}' | tee .springfield/run.json"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "mv touching control plane blocked",
			stdin:    `{"tool_input":{"command":"mv .springfield/run.json /tmp/run.json"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
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

// TestHookGuardRecursionGuard verifies the anchored recursion guard in both
// modes. Matching is command-position anchored (start-of-string or after a
// shell separator, allowing env-assignment and binary-path prefixes), so verb
// substrings inside quotes/args/messages are NOT blocked.
//
//   - Without --block-reentry (plugin hook / interactive): only `start` is
//     gated, and the SPRINGFIELD_ALLOW_START= sentinel exempts it.
//     `plan`/`recover` pass through so their skills complete.
//   - With --block-reentry (adapter-injected / subagent): start/plan/recover
//     all blocked; the sentinel is ignored.
func TestHookGuardRecursionGuard(t *testing.T) {
	bin := buildBinary(t)

	cases := []struct {
		name     string
		flags    []string // extra args after "hook-guard"
		stdin    string
		wantExit int
		wantErr  string
	}{
		// ---- interactive (no flag): only `start` gated ----
		{
			name:     "interactive blocks bare start",
			stdin:    `{"tool_input":{"command":"springfield start"}}`,
			wantExit: 2,
			wantErr:  "operator-launched",
		},
		{
			name:     "interactive blocks start with leading whitespace",
			stdin:    `{"tool_input":{"command":"  springfield start"}}`,
			wantExit: 2,
			wantErr:  "operator-launched",
		},
		{
			name:     "interactive blocks start with trailing args",
			stdin:    `{"tool_input":{"command":"springfield start --dir ."}}`,
			wantExit: 2,
			wantErr:  "operator-launched",
		},
		{
			name:     "interactive blocks start after && (second separator anchor)",
			stdin:    `{"tool_input":{"command":"cd x && springfield start"}}`,
			wantExit: 2,
			wantErr:  "operator-launched",
		},
		{
			name:     "interactive blocks start after || ",
			stdin:    `{"tool_input":{"command":"x || springfield start"}}`,
			wantExit: 2,
			wantErr:  "operator-launched",
		},
		{
			name:     "interactive blocks start with no-space &&",
			stdin:    `{"tool_input":{"command":"x&&springfield start"}}`,
			wantExit: 2,
			wantErr:  "operator-launched",
		},
		{
			name:     "interactive blocks env-prefixed start (airtight)",
			stdin:    `{"tool_input":{"command":"FOO=1 springfield start"}}`,
			wantExit: 2,
			wantErr:  "operator-launched",
		},
		{
			name:     "interactive blocks empty-value env-prefixed start",
			stdin:    `{"tool_input":{"command":"FOO= springfield start"}}`,
			wantExit: 2,
			wantErr:  "operator-launched",
		},
		{
			name:     "interactive blocks quoted-path start",
			stdin:    `{"tool_input":{"command":"\"/usr/local/bin/springfield\" start"}}`,
			wantExit: 2,
			wantErr:  "operator-launched",
		},
		{
			name:     "interactive allows start with empty-value sentinel",
			stdin:    `{"tool_input":{"command":"SPRINGFIELD_ALLOW_START= springfield start"}}`,
			wantExit: 0,
		},
		{
			name:     "interactive allows start with operator sentinel",
			stdin:    `{"tool_input":{"command":"SPRINGFIELD_ALLOW_START=1 springfield start"}}`,
			wantExit: 0,
		},
		{
			name:     "interactive allows start with sentinel any value",
			stdin:    `{"tool_input":{"command":"SPRINGFIELD_ALLOW_START=yes springfield start"}}`,
			wantExit: 0,
		},
		{
			name:     "interactive allows start with sentinel among multiple env",
			stdin:    `{"tool_input":{"command":"FOO=1 SPRINGFIELD_ALLOW_START=1 springfield start"}}`,
			wantExit: 0,
		},
		{
			// Sentinel smuggled in an unrelated prior command must NOT authorize.
			name:     "interactive blocks start with sentinel smuggled before &&",
			stdin:    `{"tool_input":{"command":"echo SPRINGFIELD_ALLOW_START=1 && springfield start"}}`,
			wantExit: 2,
			wantErr:  "operator-launched",
		},
		{
			// Wrong token (left-boundary): NOT_SPRINGFIELD_ALLOW_START is not the sentinel.
			name:     "interactive blocks start with look-alike env key",
			stdin:    `{"tool_input":{"command":"NOT_SPRINGFIELD_ALLOW_START=1 springfield start"}}`,
			wantExit: 2,
			wantErr:  "operator-launched",
		},
		{
			// Sentinel as a value, not a key, must NOT authorize.
			name:     "interactive blocks start with sentinel in env value",
			stdin:    `{"tool_input":{"command":"X=SPRINGFIELD_ALLOW_START=1 springfield start"}}`,
			wantExit: 2,
			wantErr:  "operator-launched",
		},
		{
			name:     "interactive allows plan",
			stdin:    `{"tool_input":{"command":"springfield plan --prd -"}}`,
			wantExit: 0,
		},
		{
			name:     "interactive allows recover",
			stdin:    `{"tool_input":{"command":"springfield recover --plan p"}}`,
			wantExit: 0,
		},
		{
			name:     "interactive allows status",
			stdin:    `{"tool_input":{"command":"springfield status"}}`,
			wantExit: 0,
		},
		{
			name:     "interactive allows start inside single quotes",
			stdin:    `{"tool_input":{"command":"echo 'springfield start'"}}`,
			wantExit: 0,
		},
		{
			name:     "interactive allows verb inside grep pattern",
			stdin:    `{"tool_input":{"command":"grep \"springfield plan\" f"}}`,
			wantExit: 0,
		},
		{
			name:     "interactive allows verb inside commit message",
			stdin:    `{"tool_input":{"command":"git commit -m \"ran springfield plan\""}}`,
			wantExit: 0,
		},
		{
			name:     "interactive allows unrelated bash",
			stdin:    `{"tool_input":{"command":"echo hi"}}`,
			wantExit: 0,
		},

		// ---- subagent (--block-reentry): all three gated, sentinel ignored ----
		{
			name:     "subagent blocks start",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"springfield start"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks plan",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"springfield plan"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks recover",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"springfield recover"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks plan after &&",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"cd x && springfield plan"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks path-prefixed plan",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"/usr/local/bin/springfield plan"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks backtick subshell plan",
			flags:    []string{"--block-reentry"},
			stdin:    "{\"tool_input\":{\"command\":\"`springfield plan`\"}}",
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks dollar-paren subshell plan",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"$(springfield plan)"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks env-prefixed plan (airtight)",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"FOO=1 springfield plan"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks single-quoted-path plan",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"'/opt/homebrew/bin/springfield' plan"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks quoted-name plan",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"\"springfield\" plan"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent ignores sentinel — bare start still blocked",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"SPRINGFIELD_ALLOW_START=1 springfield start"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent allows status",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"springfield status"}}`,
			wantExit: 0,
		},
		{
			name:     "subagent allows verb inside quotes",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"echo 'springfield plan'"}}`,
			wantExit: 0,
		},
		{
			// ACCEPTED RESIDUAL: bash -c hides the real invocation behind an
			// arg string; catching it needs a shell parser. Pinned as allowed
			// so the boundary is explicit (backed by plugin-disable +
			// anti-recursion prompt for subagents).
			name:     "subagent allows bash -c residual",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"bash -c \"springfield plan\""}}`,
			wantExit: 0,
		},
		{
			// ACCEPTED RESIDUAL: command wrappers that put springfield in arg
			// position (sudo/env/command/...) need a shell parser. Pinned to
			// document the boundary — open-ended wrapper list, out of scope.
			name:     "subagent allows sudo wrapper residual",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"sudo springfield plan"}}`,
			wantExit: 0,
		},
		{
			// env assignment whose VALUE is a path ending in springfield, then
			// a different command — not a springfield invocation, must NOT block.
			name:     "subagent allows env-value path ending in springfield",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"MY_BIN=/opt/springfield start"}}`,
			wantExit: 0,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, append([]string{"hook-guard"}, tc.flags...)...)
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
