package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
)

// Options configures optional adapter behaviour.
type Options struct {
	// WarnWriter receives the one-time warning emitted when no Gemini auth
	// source can be detected. Defaults to os.Stderr when nil.
	WarnWriter io.Writer
}

type adapter struct {
	lookPath agents.LookPathFunc

	// hookBin is the absolute path to the springfield binary used by the
	// BeforeTool hook installed in the Gemini system-settings override.
	hookBin string

	warnBuf io.Writer

	// authWarnOnce guards the one-time "no GEMINI_API_KEY / no cached
	// OAuth" warning.
	authWarnOnce sync.Once
}

// New constructs a gemini adapter with default options. Returns an
// agents.Commander so the runtime can build runnable commands.
func New(lookPath agents.LookPathFunc) agents.Commander {
	return NewWithOptions(lookPath, Options{})
}

// NewWithOptions constructs a gemini adapter with optional behaviour
// overrides (e.g. injecting a custom warn writer for tests).
func NewWithOptions(lookPath agents.LookPathFunc, opts Options) agents.Commander {
	if lookPath == nil {
		lookPath = osexec.LookPath
	}

	hookBin, err := os.Executable()
	if err != nil || hookBin == "" {
		// Fallback: trust PATH at hook-run time. Non-fatal — matches the
		// Claude adapter's posture.
		hookBin = "springfield"
	}

	warnBuf := opts.WarnWriter
	if warnBuf == nil {
		warnBuf = os.Stderr
	}

	return &adapter{
		lookPath: lookPath,
		hookBin:  hookBin,
		warnBuf:  warnBuf,
	}
}

func (a *adapter) ID() agents.ID {
	return agents.AgentGemini
}

func (a *adapter) Metadata() agents.Metadata {
	return agents.Metadata{
		ID:           agents.AgentGemini,
		Name:         "Gemini CLI",
		Binary:       "gemini",
		Capabilities: agents.CapabilitySet{},
	}
}

func (a *adapter) Detect(context.Context) agents.Detection {
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

// Command builds a runnable gemini invocation.
//
// IMPORTANT: we do NOT pass `-p`/`--prompt` here. Gemini CLI's `-p` takes a
// string argument; omitting the argument would consume the next flag as the
// prompt. Gemini auto-detects headless mode on non-TTY stdin, so piping the
// prompt via Stdin is sufficient.
func (a *adapter) Command(input agents.CommandInput) (coreexec.Command, error) {
	args := []string{
		"--output-format", "stream-json",
	}
	g := input.ExecutionSettings.Gemini
	if m := strings.TrimSpace(g.ApprovalMode); m != "" {
		args = append(args, "--approval-mode", m)
	}
	if s := strings.TrimSpace(g.SandboxMode); s != "" {
		args = append(args, "--sandbox", s)
	}
	if model := strings.TrimSpace(g.Model); model != "" {
		args = append(args, "--model", model)
	}

	env, err := a.commandEnv(input)
	if err != nil {
		// Fail closed — never spawn gemini without the BeforeTool hook.
		return coreexec.Command{}, err
	}

	a.maybeEmitAuthWarning()

	return coreexec.Command{
		Name:  "gemini",
		Args:  args,
		Stdin: input.Prompt,
		Dir:   input.WorkDir,
		Env:   env,
	}, nil
}

func (a *adapter) SuggestedModels() []string {
	return SuggestedModels()
}

func (a *adapter) ClassifyError(events []coreexec.Event, exitCode int, err error) agents.ErrorClass {
	if exitCode == 0 {
		return agents.ErrorClassFatal
	}
	if errors.Is(err, osexec.ErrNotFound) {
		return agents.ErrorClassRetryable
	}
	if geminiRetryableText(errorString(err)) {
		return agents.ErrorClassRetryable
	}
	for _, event := range events {
		if geminiRetryableEvent(event) {
			return agents.ErrorClassRetryable
		}
	}
	return agents.ErrorClassFatal
}

// commandEnv writes the per-invocation system-settings override file and
// returns the env map that points Gemini at it. Returns an error when the
// file cannot be written — callers MUST propagate the error and refuse to
// spawn gemini, because without the hook the control-plane guard is off.
func (a *adapter) commandEnv(input agents.CommandInput) (map[string]string, error) {
	settingsPath, err := writeSystemSettings(input.WorkDir, a.hookBin)
	if err != nil {
		return nil, fmt.Errorf(
			"gemini adapter: cannot install control-plane hook (system-settings write failed); refusing to spawn: %w",
			err,
		)
	}
	return map[string]string{
		"GEMINI_CLI_SYSTEM_SETTINGS_PATH": settingsPath,
	}, nil
}

// maybeEmitAuthWarning warns once (per adapter instance) when no Gemini auth
// source is detectable. Non-blocking — the subprocess still runs and may
// pick up auth we couldn't see (e.g. Vertex AI env vars).
func (a *adapter) maybeEmitAuthWarning() {
	if os.Getenv("GEMINI_API_KEY") != "" || os.Getenv("GOOGLE_API_KEY") != "" {
		return
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if _, statErr := os.Stat(filepath.Join(home, ".gemini", "oauth_token")); statErr == nil {
			return
		}
	}
	a.authWarnOnce.Do(func() {
		fmt.Fprintln(a.warnBuf,
			"springfield: no GEMINI_API_KEY set and no cached Google OAuth token at ~/.gemini/oauth_token — subprocess may prompt and hang",
		)
	})
}

// Positive-signal contract: ValidateResult returns nil only when the
// transcript carries an explicit success marker — at least one tool_use ID
// emitted by the agent whose paired tool_result reports is_error == false.
// Absence of failure markers is not enough; refusal-with-no-tools, all-tools-
// errored, and text-only runs all fail validation. OS exit code special
// cases (53 turn-limit, 42 rejected-input) short-circuit at the top before
// stream inspection. Process-level failures (any non-zero exit) also fail.
func (a *adapter) ValidateResult(result coreexec.Result) error {
	// OS exit code special cases — Gemini-specific, take precedence.
	switch result.ExitCode {
	case 53:
		return errors.New("gemini exceeded turn limit without completing task")
	case 42:
		return errors.New("gemini rejected input (exit 42); likely malformed prompt or missing auth")
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("gemini exited with non-zero code %d", result.ExitCode)
	}

	seenToolUseIDs := map[string]bool{}
	sawSuccessfulToolResult := false

	for _, e := range result.Events {
		if e.Type != coreexec.EventStdout {
			continue
		}
		var event geminiStreamEvent
		if err := json.Unmarshal([]byte(e.Data), &event); err != nil {
			continue
		}
		switch event.Type {
		case "tool_use":
			if event.ID != "" {
				seenToolUseIDs[event.ID] = true
			}
		case "tool_result":
			if event.IsError {
				continue
			}
			if event.ToolUseID != "" && seenToolUseIDs[event.ToolUseID] {
				sawSuccessfulToolResult = true
			}
		}
	}

	if sawSuccessfulToolResult {
		return nil
	}
	return errors.New("gemini exited without completing tool work")
}

type geminiStreamEvent struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	ToolUseID string `json:"tool_use_id"`
	IsError   bool   `json:"is_error"`
	Text      string `json:"text"`
}

var geminiRetryableNeedles = []string{
	"rate limit",
	"rate-limit",
	"too many requests",
	"429",
	"quota exceeded",
	"resource exhausted",
	"authentication",
	"unauthorized",
	"unauthenticated",
	"invalid_token",
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

func geminiRetryableText(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return false
	}
	for _, needle := range geminiRetryableNeedles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func geminiRetryableEvent(event coreexec.Event) bool {
	if event.Type == coreexec.EventStderr {
		return geminiRetryableText(event.Data)
	}
	if event.Type != coreexec.EventStdout {
		return false
	}

	var streamEvent geminiStreamEvent
	if err := json.Unmarshal([]byte(event.Data), &streamEvent); err != nil {
		return false
	}

	if streamEvent.Type != "message" {
		return false
	}

	return geminiRetryableText(streamEvent.Text)
}
