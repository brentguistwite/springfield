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

// NewHookGuardCommand returns the hidden `springfield hook-guard` subcommand
// used by the Claude PreToolUse hook. It reads a Claude tool-input JSON
// payload from stdin and exits with:
//
//   - 0 when no path-bearing field of `tool_input` references
//     `.springfield` (including when the JSON is malformed — fail-open so
//     parser confusion does not brick legitimate work; tamper detection is
//     a separate belt-and-suspenders layer).
//   - 2 with a deny message on stderr when any path-bearing field
//     (`file_path`, `notebook_path`, `command`, or `edits[*].file_path`)
//     contains the `.springfield` substring.
//
// Stdout is reserved per the Claude hook contract — this command must never
// write to it.
func NewHookGuardCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "hook-guard",
		Short:  "Internal: Claude PreToolUse hook that blocks writes to .springfield/.",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHookGuard(cmd.InOrStdin(), cmd.ErrOrStderr())
		},
	}
}

func runHookGuard(stdin io.Reader, stderr io.Writer) error {
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
		// Exit 2 is the Claude PreToolUse "deny" signal. Using os.Exit
		// here (vs. a RunE error) keeps stdout clean: cobra would write
		// the usage/err message to stderr AND exit 1.
		os.Exit(2)
	}
	if hookGuardShouldBlockRecursion(payload.ToolInput) {
		fmt.Fprintln(stderr, hookGuardRecursionMessage)
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

// hookGuardShouldBlockRecursion returns true when the tool_input's "command"
// field matches a springfield start/plan/recover invocation. This prevents
// subagents from re-entering the Springfield control plane via Bash tool calls.
//
// The regex intentionally matches "springfield start" anywhere in the command
// string, including inside quotes (accepted false positive — no subagent Bash
// line should contain these strings even in quotes; shell-parsing is costlier).
func hookGuardShouldBlockRecursion(toolInput map[string]any) bool {
	cmd, _ := toolInput["command"].(string)
	return regexp.MustCompile(`springfield\s+(start|plan|recover)\b`).MatchString(cmd)
}
