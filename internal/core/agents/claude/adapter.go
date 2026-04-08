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

	return coreexec.Command{
		Name: "claude",
		Args: args,
		Dir:  input.WorkDir,
	}
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
