package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	osexec "os/exec"
	"strings"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
)

type adapter struct {
	lookPath agents.LookPathFunc
}

func New(lookPath agents.LookPathFunc) agents.Commander {
	if lookPath == nil {
		lookPath = osexec.LookPath
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
	case errors.Is(err, osexec.ErrNotFound):
		result.Status = agents.DetectionStatusMissing
	default:
		result.Status = agents.DetectionStatusUnhealthy
	}

	return result
}

func (a adapter) Command(input agents.CommandInput) (coreexec.Command, error) {
	args := []string{"exec", "--json"}
	if model := strings.TrimSpace(input.ExecutionSettings.Codex.Model); model != "" {
		args = append(args, "--model", model)
	}
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
	}, nil
}

func (a adapter) SuggestedModels() []string {
	return SuggestedModels()
}

func (a adapter) ClassifyError(events []coreexec.Event, exitCode int, err error) agents.ErrorClass {
	if exitCode == 0 {
		return agents.ErrorClassFatal
	}
	if errors.Is(err, osexec.ErrNotFound) {
		return agents.ErrorClassRetryable
	}
	if codexRetryableText(errorString(err)) {
		return agents.ErrorClassRetryable
	}
	for _, event := range events {
		if codexRetryableText(event.Data) {
			return agents.ErrorClassRetryable
		}
	}
	return agents.ErrorClassFatal
}

// Positive-signal contract: ValidateResult returns nil only when the
// transcript contains at least one item.completed event whose item.type is a
// real tool/function-call (i.e. neither agent_message nor reasoning) and no
// FATAL stderr appeared during the run. Text-only sessions are failures
// under Policy A — Codex must take action to count as success.
func (a adapter) ValidateResult(result coreexec.Result) error {
	if result.ExitCode != 0 {
		return fmt.Errorf("codex exited with non-zero code %d", result.ExitCode)
	}

	hasWork := false

	for _, e := range result.Events {
		switch e.Type {
		case coreexec.EventStderr:
			if isFatalCodexStderr(e.Data) {
				return fmt.Errorf("codex reported fatal error: %s", truncate(e.Data, 200))
			}
		case coreexec.EventStdout:
			workSeen, _ := inspectCodexStdout(e.Data)
			if workSeen {
				hasWork = true
			}
		}
	}

	if !hasWork {
		return errors.New("codex exited without taking action")
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
	Type     string `json:"type"`
	Text     string `json:"text"`
	ExitCode *int   `json:"exit_code"`
}

// inspectCodexStdout reports whether a streamed Codex item.completed event
// represents successful work (returns hasWork=true). Positive-signal contract:
// a tool item only counts as success when its exit_code is zero (or absent,
// for tool types that do not expose one). Tool items with a non-zero exit_code
// are treated as failed work — the shell command ran but errored — and do NOT
// satisfy the contract on their own.
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
		// Tool item. If it carries an exit_code, require it to be 0 to
		// count as work. Item types without an exit_code field (e.g.
		// file_change) report success implicitly via item.completed.
		if item.ExitCode != nil && *item.ExitCode != 0 {
			return false, false
		}
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

var codexRetryableNeedles = []string{
	"rate limit",
	"rate-limit",
	"too many requests",
	"429",
	"quota exceeded",
	"resource exhausted",
	"authrequired",
	"invalid_token",
	"missing or invalid access token",
	"unauthorized",
	"unauthenticated",
	"401",
	"timed out",
	"timeout",
	"temporary failure",
	"temporarily unavailable",
	"service unavailable",
	"connection reset",
	"connection refused",
	"econnreset",
	"econnrefused",
	"503",
	"500",
	"overloaded",
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func codexRetryableText(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return false
	}
	for _, needle := range codexRetryableNeedles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
