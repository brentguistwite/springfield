package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// hookGuardToken is the substring that marks a Springfield control-plane
// path. Any path-bearing field on Claude's tool_input that contains this
// substring causes the hook to block the tool call.
const hookGuardToken = ".springfield"

// hookGuardBlockMessage is written to stderr when the guard blocks a call.
// Claude's PreToolUse contract treats stderr + exit 2 as a deny with reason.
const hookGuardBlockMessage = "Springfield control plane is off-limits"

// hookGuardRecursionMessage is written to stderr when the guard blocks a
// nested springfield CLI invocation in a subagent Bash tool call.
const hookGuardRecursionMessage = "Nested springfield CLI invocation blocked. Subagents must not re-enter springfield."

// hookGuardStartMessage is written to stderr when the interactive guard
// blocks a `springfield start`. Launching a batch is operator-triggered; the
// message names the sentinel the agent prepends when the operator authorizes.
const hookGuardStartMessage = "`springfield start` is operator-launched. If the user explicitly authorized this run, re-run as `SPRINGFIELD_ALLOW_START=1 springfield start ...`; otherwise ask the operator to run it."

// hookGuardStartSentinel is the env-assignment the agent prepends when the
// operator has explicitly authorized an interactive `springfield start`. Any
// value authorizes — presence of the assignment is the signal (so `=1`, `=10`,
// etc. are all equivalent; there is no partial-value ambiguity). It is a
// deliberate-action opt-in (like the jira-gate `NOTRACK=`), not an
// adversarial sandbox: a PreToolUse hook cannot read conversation intent.
const hookGuardStartSentinel = "SPRINGFIELD_ALLOW_START="

// hookGuardRecursionRegex builds the anchored recursion matcher for the given
// verb alternation. A verb only matches when `springfield` is at a shell
// command position: start-of-string or right after a separator (`;&|(){}`,
// backtick, newline), allowing optional env-assignment prefixes
// (`FOO=bar `) and an optional binary path prefix. This rejects substring
// mentions inside quotes/args/messages (the historical false positives) while
// still catching real invocations — including env-prefixed ones, so a bare
// `FOO=1 springfield plan` cannot evade it.
//
// Residuals that would require a shell parser are intentionally NOT caught:
// `bash -c "springfield plan"` and env values containing whitespace
// (`FOO="a b" springfield plan`). The guard stays a simple, dependency-free,
// fail-open regex.
func hookGuardRecursionRegex(verbs string) *regexp.Regexp {
	// Built as an interpreted string: the pattern contains a backtick, so it
	// cannot be a Go raw-string literal.
	return regexp.MustCompile("(^|[;&|(){}" + "`" + `\n])[ \t]*(\w+=\S+[ \t]+)*([^ \t'"|;&(){}` + "`" + `]*/)?springfield[ \t]+(` + verbs + `)\b`)
}

var (
	// hookGuardReentryRegex blocks all three mutating verbs — used in
	// subagent (--block-reentry) mode.
	hookGuardReentryRegex = hookGuardRecursionRegex("start|plan|recover")
	// hookGuardStartRegex blocks only `start` — used in interactive mode,
	// where `plan`/`recover` are legitimate skill-driven invocations.
	hookGuardStartRegex = hookGuardRecursionRegex("start")
)

// NewHookGuardCommand returns the hidden `springfield hook-guard` subcommand
// used by the agent PreToolUse hooks. It reads a tool-input JSON payload from
// stdin and exits with:
//
//   - 0 when nothing is blocked (including when the JSON is malformed —
//     fail-open so parser confusion does not brick legitimate work).
//   - 2 with a deny message on stderr when a path-bearing field targets
//     `.springfield`, or when a recursion verb is invoked at a command
//     position.
//
// Recursion scope depends on --block-reentry:
//   - without the flag (plugin hook, interactive session): blocks only
//     `springfield start`, and exempts it when the operator sentinel
//     (SPRINGFIELD_ALLOW_START=) is present. `plan`/`recover` pass through so
//     their skills can complete.
//   - with the flag (adapter-injected, subagent): blocks start/plan/recover
//     unconditionally — full re-entry protection. The sentinel is ignored.
//
// Stdout is reserved per the hook contract — this command must never write to
// it.
func NewHookGuardCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "hook-guard",
		Short:  "Internal: agent PreToolUse hook that guards the .springfield/ control plane.",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			blockReentry, _ := cmd.Flags().GetBool("block-reentry")
			return runHookGuard(cmd.InOrStdin(), cmd.ErrOrStderr(), blockReentry)
		},
	}
	cmd.Flags().Bool("block-reentry", false,
		"block all springfield start/plan/recover invocations (subagent mode); "+
			"without it, only interactive `start` is gated")
	return cmd
}

func runHookGuard(stdin io.Reader, stderr io.Writer, blockReentry bool) error {
	data, err := io.ReadAll(stdin)
	if err != nil {
		// Unable to read stdin — fail-open.
		return nil
	}

	var payload struct {
		ToolInput map[string]any `json:"tool_input"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		// Malformed JSON — fail-open (see doc comment).
		return nil
	}

	if hookGuardShouldBlock(payload.ToolInput) {
		fmt.Fprintln(stderr, hookGuardBlockMessage)
		// Exit 2 is the PreToolUse "deny" signal. Using os.Exit here (vs. a
		// RunE error) keeps stdout clean: cobra would otherwise write the
		// usage/err message and exit 1.
		os.Exit(2)
	}

	cmd, _ := payload.ToolInput["command"].(string)
	if blockReentry {
		if hookGuardReentryRegex.MatchString(cmd) {
			fmt.Fprintln(stderr, hookGuardRecursionMessage)
			os.Exit(2)
		}
		return nil
	}
	// Interactive mode: gate only `start`, exempt it when the operator
	// sentinel is present.
	if !strings.Contains(cmd, hookGuardStartSentinel) && hookGuardStartRegex.MatchString(cmd) {
		fmt.Fprintln(stderr, hookGuardStartMessage)
		os.Exit(2)
	}
	return nil
}

// hookGuardShouldBlock returns true when any path-bearing field in the
// tool_input map contains the `.springfield` substring.
func hookGuardShouldBlock(toolInput map[string]any) bool {
	if toolInput == nil {
		return false
	}
	// Direct path-bearing fields.
	for _, key := range []string{"file_path", "notebook_path", "command"} {
		if s, ok := toolInput[key].(string); ok && strings.Contains(s, hookGuardToken) {
			return true
		}
	}
	// MultiEdit: edits is an array of {file_path, ...} entries.
	if raw, ok := toolInput["edits"].([]any); ok {
		for _, e := range raw {
			entry, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if s, ok := entry["file_path"].(string); ok && strings.Contains(s, hookGuardToken) {
				return true
			}
		}
	}
	return false
}
