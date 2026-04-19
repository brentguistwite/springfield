package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
)

type adapter struct {
	lookPath agents.LookPathFunc
}

func New(lookPath agents.LookPathFunc) agents.Commander {
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	return adapter{lookPath: lookPath}
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
	// PreToolUse hook. A lexical deny list is bypassed by absolute paths,
	// cd, and shell redirects; a substring grep catches all those forms.
	args = append(args, "--settings", springfieldControlPlaneSettingsJSON())

	return coreexec.Command{
		Name: "claude",
		Args: args,
		Dir:  input.WorkDir,
	}
}

// springfieldControlPlaneHookCommand is the shell one-liner executed by
// Claude's PreToolUse hook. It reads the tool-input JSON from stdin and
// exits 2 (blocking the tool call) if any substring of it references
// .springfield. Exit 0 allows the call through.
const springfieldControlPlaneHookCommand = `grep -q '\.springfield' && { echo 'Springfield control plane is off-limits' >&2; exit 2; } || exit 0`

// SpringfieldControlPlaneHookCommand returns the hook command string used
// in the --settings JSON. Exported for tests pinning the shell-level
// behavior of the hook.
func SpringfieldControlPlaneHookCommand() string {
	return springfieldControlPlaneHookCommand
}

// springfieldControlPlaneSettingsJSON returns the inline --settings payload
// registering the PreToolUse hook that protects .springfield/ from agent
// writes.
func springfieldControlPlaneSettingsJSON() string {
	payload := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []map[string]any{{
				"matcher": "Write|Edit|MultiEdit|NotebookEdit|Bash",
				"hooks": []map[string]any{{
					"type":    "command",
					"command": springfieldControlPlaneHookCommand,
				}},
			}},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		// payload is static — marshal errors are impossible in practice,
		// but fall back to a hand-built string rather than panic.
		return `{"hooks":{"PreToolUse":[{"matcher":"Write|Edit|MultiEdit|NotebookEdit|Bash","hooks":[{"type":"command","command":"` + springfieldControlPlaneHookCommand + `"}]}]}}`
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
