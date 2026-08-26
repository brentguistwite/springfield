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
		// --- agent-harness config surfaces (.opencode/, opencode.json) ---
		// Planting a project plugin or config is itself the bypass: opencode
		// loads both at boot, executing attacker JS ahead of any tool-call
		// hook. Writes into these surfaces are blocked outright.
		{
			name:     "write planted plugin into project .opencode blocked",
			stdin:    `{"tool_input":{"file_path":".opencode/plugins/helpers.js","content":"pwned"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "nested .opencode path blocked",
			stdin:    `{"tool_input":{"file_path":"/abs/worktree/.opencode/config.json"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "notebook_path into .opencode blocked",
			stdin:    `{"tool_input":{"notebook_path":".opencode/nb.ipynb"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "multiedit edits array into .opencode blocked",
			stdin:    `{"tool_input":{"edits":[{"file_path":".opencode/agent.md"}]}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "redirect into project opencode.json blocked",
			stdin:    `{"tool_input":{"command":"echo x > opencode.json"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "rm -rf .opencode blocked",
			stdin:    `{"tool_input":{"command":"rm -rf .opencode"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "mutation of .opencode file via mv blocked",
			stdin:    `{"tool_input":{"command":"mv evil.js .opencode/plugins/helpers.js"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			// Reads and mentions stay allowed — same posture as .springfield.
			name:     "reading .opencode plugin allowed",
			stdin:    `{"tool_input":{"command":"cat .opencode/plugins/helpers.js"}}`,
			wantExit: 0,
		},
		{
			name:     "benign path merely containing 'opencode' allowed",
			stdin:    `{"tool_input":{"file_path":"src/opencode-notes.md"}}`,
			wantExit: 0,
		},
		{
			name:     "benign command writing elsewhere allowed",
			stdin:    `{"tool_input":{"command":"echo hi > notes.txt"}}`,
			wantExit: 0,
		},
		// --- case-slip hardening (F3): macOS APFS is case-insensitive, so a
		// case-folded path REALLY hits the control plane. Must deny. ---
		{
			name:     "uppercase .SPRINGFIELD rm blocked",
			stdin:    `{"tool_input":{"command":"rm -rf .SPRINGFIELD/*"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "mixed-case file_path blocked",
			stdin:    `{"tool_input":{"file_path":".Springfield/run.json"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			name:     "mixed-case redirect blocked",
			stdin:    `{"tool_input":{"command":"echo pwn > .SpringField/x"}}`,
			wantExit: 2,
			wantErr:  "off-limits",
		},
		{
			// Reads stay reads regardless of case.
			name:     "read of uppercase .SPRINGFIELD path allowed",
			stdin:    `{"tool_input":{"command":"cat .SPRINGFIELD/run.json"}}`,
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
			// BOUNDED UNWRAP (F4): sh/bash/zsh -c puts springfield in arg
			// position behind a known wrapper; one unwrap level catches it.
			name:     "subagent blocks bash -c wrapper",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"bash -c \"springfield plan\""}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks sh -c start",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"sh -c \"springfield start\""}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks zsh -c recover",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"zsh -c 'springfield recover'"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks env wrapper",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"env springfield start"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks env wrapper with assignment",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"env FOO=1 springfield plan"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks command wrapper",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"command springfield plan"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks exec wrapper",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"exec springfield recover"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks sudo wrapper",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"sudo springfield plan"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks sudo with path-prefixed binary",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"sudo /usr/local/bin/springfield recover"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks nohup wrapper",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"nohup springfield plan"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks timeout with duration arg",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"timeout 30 springfield plan"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks nice with -n value",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"nice -n 5 springfield plan"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			name:     "subagent blocks wrapper with leading assignment",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"FOO=1 sudo springfield plan"}}`,
			wantExit: 2,
			wantErr:  "Nested springfield CLI invocation blocked",
		},
		{
			// DOCUMENTED RESIDUAL: the bounded unwrap applies ONE extra level.
			// A wrapper chain nested inside the payload still hides the verb.
			name:     "subagent allows deeper wrapper chain residual",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"bash -c \"sh -c 'springfield plan'\""}}`,
			wantExit: 0,
		},
		{
			// DOCUMENTED RESIDUAL: only the FIRST token's wrapper is unwrapped;
			// wrappers after a separator stay hidden (needs a shell parser).
			name:     "subagent allows mid-command wrapper after separator residual",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"cd x && sudo springfield plan"}}`,
			wantExit: 0,
		},
		{
			// Wrappers wrapping NON-springfield work must not block.
			name:     "subagent allows bash -c around benign command",
			flags:    []string{"--block-reentry"},
			stdin:    `{"tool_input":{"command":"bash -c \"echo hi\""}}`,
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
