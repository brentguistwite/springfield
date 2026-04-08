package codex

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
	return agents.AgentCodex
}

func (a adapter) Metadata() agents.Metadata {
	return agents.Metadata{
		ID:           agents.AgentCodex,
		Name:         "Codex CLI",
		Binary:       "codex",
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
	args := []string{"exec", "--json"}
	if sandboxMode := strings.TrimSpace(input.ExecutionSettings.Codex.SandboxMode); sandboxMode != "" {
		args = append(args, "-s", sandboxMode)
	}
	if approvalPolicy := strings.TrimSpace(input.ExecutionSettings.Codex.ApprovalPolicy); approvalPolicy != "" {
		args = append(args, "-a", approvalPolicy)
	}
	args = append(args, input.Prompt)

	return coreexec.Command{
		Name: "codex",
		Args: args,
		Dir:  input.WorkDir,
	}
}

// ValidateResult checks Codex stderr for fatal errors that indicate the session
// didn't complete real work despite exit code 0.
func (a adapter) ValidateResult(result coreexec.Result) error {
	hasWork := false
	askedClarifyingQuestion := false

	for _, e := range result.Events {
		switch e.Type {
		case coreexec.EventStderr:
			if isFatalCodexStderr(e.Data) {
				return fmt.Errorf("codex reported fatal error: %s", truncate(e.Data, 200))
			}
		case coreexec.EventStdout:
			workSeen, questionSeen := inspectCodexStdout(e.Data)
			if workSeen {
				hasWork = true
			}
			if questionSeen {
				askedClarifyingQuestion = true
			}
		}
	}

	if askedClarifyingQuestion && !hasWork {
		return fmt.Errorf("codex asked a clarifying question without completing work")
	}

	return nil
}

func isFatalCodexStderr(data string) bool {
	if !strings.Contains(data, "FATAL") && !strings.Contains(data, "fatal:") {
		return false
	}
	lower := strings.ToLower(data)
	if strings.Contains(lower, "authrequired") || strings.Contains(lower, "invalid_token") {
		return false
	}
	return true
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

type codexStreamEvent struct {
	Type string          `json:"type"`
	Item json.RawMessage `json:"item"`
}

type codexStreamItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func inspectCodexStdout(data string) (hasWork bool, askedClarifyingQuestion bool) {
	var event codexStreamEvent
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return false, false
	}

	if event.Type != "item.completed" || len(event.Item) == 0 {
		return false, false
	}

	var item codexStreamItem
	if err := json.Unmarshal(event.Item, &item); err != nil {
		return false, false
	}

	if item.Type != "" && item.Type != "agent_message" && item.Type != "reasoning" {
		return true, false
	}

	if item.Type == "agent_message" && looksLikeClarifyingQuestion(item.Text) {
		return false, true
	}

	return false, false
}

func looksLikeClarifyingQuestion(text string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(text))
	if trimmed == "" || !strings.Contains(trimmed, "?") {
		return false
	}

	for _, prefix := range []string{
		"what ",
		"which ",
		"where ",
		"when ",
		"why ",
		"how ",
		"can you ",
		"could you ",
		"would you ",
		"do you want ",
		"should i ",
	} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}

	return strings.Contains(trimmed, "clarif")
}
