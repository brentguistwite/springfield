package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
)

type adapter struct {
	lookPath agents.LookPathFunc
	// hookBin is the absolute path to the springfield binary used by the
	// PreToolUse hook. Resolved at construction via os.Executable() so the
	// hook always invokes the same binary the user launched, regardless of
	// PATH shuffles in child processes. If resolution fails, falls back to
	// the bare name "springfield" for PATH lookup at hook time.
	hookBin string
}

func New(lookPath agents.LookPathFunc) agents.Commander {
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	hookBin, err := os.Executable()
	if err != nil || hookBin == "" {
		// Fallback: trust PATH at hook-run time. Non-fatal.
		hookBin = "springfield"
	}

	return adapter{lookPath: lookPath, hookBin: hookBin}
}

func (a adapter) ID() agents.ID {
	return agents.AgentClaude
}

func (a adapter) Metadata() agents.Metadata {
	return agents.Metadata{
		ID:           agents.AgentClaude,
		Name:         "Claude Code",
		Binary:       "claude",
		Capabilities: agents.CapabilitySet{},
	}
}

func (a adapter) Detect(context.Context) agents.Detection {
	metadata := a.Metadata()
	path, err := a.lookPath(metadata.Binary)

	result := agents.Detection{
		ID:     metadata.ID,
		Name:   metadata.Name,
		Binary: metadata.Binary,
		Path:   path,
		Err:    err,
	}

	switch {
	case err == nil:
		result.Status = agents.DetectionStatusAvailable
	case errors.Is(err, exec.ErrNotFound):
		result.Status = agents.DetectionStatusMissing
	default:
		result.Status = agents.DetectionStatusUnhealthy
	}

	return result
}

func (a adapter) Command(input agents.CommandInput) coreexec.Command {
	args := []string{
		"-p", input.Prompt,
		"--output-format", "stream-json",
		"--verbose",
	}
	if permissionMode := strings.TrimSpace(input.ExecutionSettings.Claude.PermissionMode); permissionMode != "" {
		args = append(args, "--permission-mode", permissionMode)
	}

	// Hard-block agent writes to Springfield's control plane with a
	// PreToolUse hook. The hook command invokes `springfield hook-guard`,
	// which inspects path-bearing fields of the tool_input JSON on stdin
	// and exits 2 when any of them target .springfield/. Path-aware (vs.
	// substring grep) so legitimate edits whose *content* merely mentions
	// .springfield are allowed through.
	args = append(args, "--settings", a.springfieldControlPlaneSettingsJSON())

	return coreexec.Command{
		Name: "claude",
		Args: args,
		Dir:  input.WorkDir,
	}
}

// SpringfieldControlPlaneHookCommand returns the hook command string used
// in the --settings JSON. Exposed as an instance method because the command
// embeds the resolved springfield binary path (see adapter.hookBin).
func (a adapter) SpringfieldControlPlaneHookCommand() string {
	// Quote the binary path so paths with spaces survive shell parsing.
	// The hook-guard subcommand never touches the shell itself; the quoting
	// matters for Claude's shell-based hook runner.
	return shellQuote(a.hookBin) + " hook-guard"
}

// shellQuote wraps s in single quotes, escaping any embedded single quotes.
// Used for the PreToolUse hook command string so paths with spaces/quotes
// survive the shell layer.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// springfieldControlPlaneSettingsJSON returns the inline --settings payload
// registering the PreToolUse hook that protects .springfield/ from agent
// writes.
func (a adapter) springfieldControlPlaneSettingsJSON() string {
	hookCommand := a.SpringfieldControlPlaneHookCommand()
	payload := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []map[string]any{{
				"matcher": "Write|Edit|MultiEdit|NotebookEdit|Bash",
				"hooks": []map[string]any{{
					"type":    "command",
					"command": hookCommand,
				}},
			}},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		// payload is static — marshal errors are impossible in practice,
		// but fall back to a hand-built string rather than panic.
		return `{"hooks":{"PreToolUse":[{"matcher":"Write|Edit|MultiEdit|NotebookEdit|Bash","hooks":[{"type":"command","command":"` + hookCommand + `"}]}]}}`
	}
	return string(data)
}

// ValidateResult checks Claude's stream-json output for rejected tool calls,
// which indicate the agent couldn't complete the task autonomously.
func (a adapter) ValidateResult(result coreexec.Result) error {
	for _, e := range result.Events {
		if e.Type != coreexec.EventStdout {
			continue
		}
		if isClaudeRejectedToolCall(e.Data) {
			return fmt.Errorf("claude had rejected tool calls (agent may have asked questions instead of completing work)")
		}
	}
	return nil
}

type claudeStreamEvent struct {
	Type    string `json:"type"`
	Message struct {
		Content []claudeMessageContent `json:"content"`
	} `json:"message"`
}

type claudeMessageContent struct {
	Type    string `json:"type"`
	IsError bool   `json:"is_error"`
	Content any    `json:"content"`
}

func isClaudeRejectedToolCall(data string) bool {
	var event claudeStreamEvent
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return false
	}

	for _, item := range event.Message.Content {
		if item.Type != "tool_result" || !item.IsError {
			continue
		}
		if isClaudeRejectionContent(item.Content) {
			return true
		}
	}

	return false
}

func isClaudeRejectionContent(content any) bool {
	text := strings.ToLower(agents.FlattenJSONText(content))
	return strings.Contains(text, "rejected") || strings.Contains(text, "denied")
}
