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
//
// It is honored ONLY when it appears as a real leading env-assignment of the
// command-position `springfield start` invocation (see
// hookGuardAuthorizedStartRegex) — NOT as a bare substring anywhere in the
// command. A substring check would let `echo SPRINGFIELD_ALLOW_START=1 &&
// springfield start` (sentinel in an unrelated command) or
// `NOT_SPRINGFIELD_ALLOW_START=1 ...` (wrong token) forge authorization.
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
// any wrapper that puts `springfield` in argument position rather than command
// position — `bash -c "springfield plan"`, `sh -c ...`, `env springfield
// plan`, `command springfield plan`, `sudo springfield plan`, `xargs`, etc. —
// and env values containing whitespace (`FOO="a b" springfield plan`). The
// wrapper list is open-ended, so a complete fix needs a shell parser; that is
// deliberately out of scope. The guard stays a simple, dependency-free,
// fail-open regex, and subagents are covered by two further layers
// (permissions.deny strips Task/Workflow; the springfield plugin is disabled;
// the executionPrompt carries an anti-recursion contract).
func hookGuardRecursionRegex(verbs string) *regexp.Regexp {
	return regexp.MustCompile(hookGuardSep + hookGuardEnv + hookGuardBinSpringfield + `[ \t]+(` + verbs + `)\b`)
}

// Regex building blocks (interpreted strings — the patterns embed a backtick,
// so they cannot be Go raw-string literals):
//
//   - hookGuardSep:           start-of-string or a shell command separator
//     (`;&|(){}`, backtick, newline) plus following whitespace.
//   - hookGuardEnv:           zero or more leading env-assignments
//     (`FOO=bar `, also empty-value `FOO= `).
//   - hookGuardBinSpringfield: the `springfield` token, optionally wrapped in
//     quotes and/or carrying a binary-path prefix — so `"/opt/bin/springfield"`
//     and `'springfield'` are recognized, not just bare `springfield`.
const (
	hookGuardSep = "(^|[;&|(){}" + "`" + `\n])[ \t]*`
	// Env names follow shell grammar ([A-Za-z_]\w*) so a non-assignment word
	// like `123=val` isn't mistaken for an env prefix.
	hookGuardEnv = `([A-Za-z_]\w*=\S*[ \t]+)*`
	// The path char-class excludes `=` so a `KEY=/path/springfield` env
	// assignment (followed by a different command) is not consumed as a
	// binary-path prefix to `springfield` — that would be a false positive.
	hookGuardBinSpringfield = `["']?([^ \t'"|;&(){}=` + "`" + `]*/)?springfield["']?`
)

var (
	// hookGuardReentryRegex blocks all three mutating verbs — used in
	// subagent (--block-reentry) mode.
	hookGuardReentryRegex = hookGuardRecursionRegex("start|plan|recover")
	// hookGuardStartRegex blocks only `start` — used in interactive mode,
	// where `plan`/`recover` are legitimate skill-driven invocations.
	hookGuardStartRegex = hookGuardRecursionRegex("start")
	// hookGuardAuthorizedStartRegex matches a command-position `springfield
	// start` whose leading env-assignment prefix includes the operator
	// sentinel. Honoring the sentinel only when it sits in this position (not
	// anywhere in the command) defeats smuggling/forging it via an unrelated
	// token. The sentinel key must be a whole token, so `NOT_SPRINGFIELD_..`
	// or `X=SPRINGFIELD_ALLOW_START=..` (value, not key) do not authorize.
	hookGuardAuthorizedStartRegex = regexp.MustCompile(
		hookGuardSep + hookGuardEnv + regexp.QuoteMeta(hookGuardStartSentinel) + `\S*[ \t]+` +
			hookGuardEnv + hookGuardBinSpringfield + `[ \t]+start\b`)

	// hookGuardRedirectRegex matches a shell redirection whose target is a
	// .springfield path: `> .springfield/x`, `>>.springfield/x`, `2> .springfield`,
	// `&> .springfield`. This is the redirect half of command-field mutation
	// detection (see hookGuardCommandMutatesControlPlane).
	hookGuardRedirectRegex = regexp.MustCompile(
		`(\d*>>?|&>>?)[ \t]*["']?[^ \t"'|;&<>` + "`" + `]*\.springfield`)

	// hookGuardMutationCmdRegex matches a state-mutating command at shell
	// command-position (start-of-string or after a separator, env-prefix and
	// binary-path aware, same anchoring as the recursion guard). Paired with a
	// `.springfield` substring test, this catches `rm -rf .springfield/x`,
	// `mv .springfield/x y`, `cd .springfield && rm run.json`, etc. while
	// letting reads (`cat`, `ls`, `grep`) and prose mentions pass.
	hookGuardMutationCmdRegex = regexp.MustCompile(
		hookGuardSep + hookGuardEnv + `["']?([^ \t'"|;&(){}=` + "`" + `]*/)?(` + hookGuardMutationVerbs + `)\b`)
)

// hookGuardMutationVerbs is the alternation of shell commands that write or
// delete filesystem state. Read-only tools (cat/ls/grep/rg/head/tail/find,
// and sed/awk without in-place flags) are deliberately excluded so they do not
// trip the guard when merely inspecting a .springfield path. Residuals that
// need a shell parser are accepted, consistent with the recursion guard:
// in-place edits via `sed -i`/`perl -i`, and binary wrappers
// (`bash -c`, `env`, `sudo`, `xargs`) are NOT caught.
const hookGuardMutationVerbs = `rm|rmdir|mv|cp|tee|truncate|dd|install|ln|mkdir|touch|chmod|chown|shred|unlink`

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
	// Interactive mode: gate only `start`, exempt it only when the operator
	// sentinel sits in the env-prefix of the actual `springfield start`
	// invocation (not merely somewhere in the command string).
	if hookGuardStartRegex.MatchString(cmd) && !hookGuardAuthorizedStartRegex.MatchString(cmd) {
		fmt.Fprintln(stderr, hookGuardStartMessage)
		os.Exit(2)
	}
	return nil
}

// hookGuardShouldBlock returns true when a tool call would mutate the
// .springfield control plane. File-path fields (Write/Edit/MultiEdit) are
// inherently writes, so a `.springfield` substring there blocks unconditionally.
// The Bash `command` field is different: it is blocked only when it MUTATES a
// .springfield path (redirect target or a mutation command), not when it merely
// mentions one — so commit bodies, greps, and reads that reference the path are
// no longer false-positived (see hookGuardCommandMutatesControlPlane).
func hookGuardShouldBlock(toolInput map[string]any) bool {
	if toolInput == nil {
		return false
	}
	// Write/Edit path fields: any .springfield path is a mutation.
	for _, key := range []string{"file_path", "notebook_path"} {
		if s, ok := toolInput[key].(string); ok && strings.Contains(s, hookGuardToken) {
			return true
		}
	}
	// Bash command: block only mutations, not mentions/reads.
	if s, ok := toolInput["command"].(string); ok && hookGuardCommandMutatesControlPlane(s) {
		return true
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

// hookGuardCommandMutatesControlPlane reports whether a Bash command string
// writes to or deletes a .springfield path, as opposed to merely mentioning one
// (in a commit/PR body, a grep pattern, or a read). It requires a `.springfield`
// substring AND either a redirection into such a path or a mutation command at
// shell command-position. Reads (cat/ls/grep) and prose mentions return false.
func hookGuardCommandMutatesControlPlane(cmd string) bool {
	if !strings.Contains(cmd, hookGuardToken) {
		return false
	}
	return hookGuardRedirectRegex.MatchString(cmd) || hookGuardMutationCmdRegex.MatchString(cmd)
}
