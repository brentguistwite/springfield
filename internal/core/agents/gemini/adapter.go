package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
		lookPath = exec.LookPath
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
	case errors.Is(err, exec.ErrNotFound):
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

// ValidateResult inspects a completed gemini run and returns an error when
// the run failed silently (e.g. exit 0 but only a clarifying question was
// emitted), or when OS exit codes indicate Gemini-specific failure modes.
//
// Field-shape notes (synthetic, verified against Gemini CLI v0.39 docs —
// docs/cli/tutorials/automation.md + docs/hooks/reference.md):
//   - error event:         {"type":"error","message":"..."}
//   - tool_result event:   {"type":"tool_result","is_error":bool,"content":...}
//   - result event:        {"type":"result",...}
//   - message event:       {"type":"message","content":...}
//   - tool_use event:      {"type":"tool_use",...}
//
// Fixtures under tests/agents/fixtures/gemini/ exercise each path. The
// synthetic-fixture caveat: field names should be reconfirmed during F.4.
func (a *adapter) ValidateResult(result coreexec.Result) error {
	// Map OS exit codes Gemini documents.
	switch result.ExitCode {
	case 53:
		return errors.New("gemini exceeded turn limit without completing task")
	case 42:
		return errors.New("gemini rejected input (exit 42); likely malformed prompt or missing auth")
	}

	hasWork := false
	sawError := ""
	sawDenied := false
	onlyClarifyingQuestion := false

	for _, e := range result.Events {
		if e.Type != coreexec.EventStdout {
			continue
		}
		inspectGeminiStreamLine(e.Data, &hasWork, &sawError, &sawDenied, &onlyClarifyingQuestion)
	}

	if sawError != "" {
		return fmt.Errorf("gemini reported error: %s", sawError)
	}
	if sawDenied {
		return errors.New("gemini had denied tool calls (agent may have asked questions instead of completing work)")
	}

	// Exit 1 with no work event → treat as failure.
	if result.ExitCode == 1 && !hasWork {
		return errors.New("gemini exited with code 1 before completing any work")
	}

	// Clean exit but only a clarifying question and no real work → failure.
	if result.ExitCode == 0 && onlyClarifyingQuestion && !hasWork {
		return errors.New("gemini asked a clarifying question without completing work")
	}

	return nil
}

type geminiStreamEvent struct {
	Type     string          `json:"type"`
	Message  string          `json:"message"`
	IsError  bool            `json:"is_error"`
	Content  json.RawMessage `json:"content"`
	Text     string          `json:"text"`
	ToolName string          `json:"tool_name"`
}

func inspectGeminiStreamLine(data string, hasWork *bool, sawError *string, sawDenied *bool, onlyClarifyingQuestion *bool) {
	var event geminiStreamEvent
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return
	}

	switch event.Type {
	case "error":
		if event.Message != "" && *sawError == "" {
			*sawError = event.Message
		}
	case "tool_use":
		*hasWork = true
	case "tool_result":
		if event.IsError && isGeminiDeniedContent(event.Content) {
			*sawDenied = true
			return
		}
		*hasWork = true
	case "message":
		// Only count as clarifying-question if no prior work event has
		// been seen for this stream. If the model later emits a tool_use
		// the hasWork flag flips and this will be ignored.
		text := agentMessageText(event)
		if !*hasWork && looksLikeClarifyingQuestion(text) {
			*onlyClarifyingQuestion = true
		}
	}
}

func agentMessageText(ev geminiStreamEvent) string {
	if ev.Text != "" {
		return ev.Text
	}
	if len(ev.Content) > 0 {
		var raw any
		if err := json.Unmarshal(ev.Content, &raw); err == nil {
			return agents.FlattenJSONText(raw)
		}
	}
	return ""
}

func isGeminiDeniedContent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var any_ any
	if err := json.Unmarshal(raw, &any_); err != nil {
		return true
	}
	text := strings.ToLower(agents.FlattenJSONText(any_))
	return strings.Contains(text, "denied") || strings.Contains(text, "rejected")
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
