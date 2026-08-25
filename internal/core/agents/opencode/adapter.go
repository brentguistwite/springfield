// Package opencode adapts the OpenCode CLI ("opencode run") for Springfield
// batch execution, mirroring the gemini adapter's fail-closed control-plane
// injection posture.
package opencode

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
	// WarnWriter receives the one-time warning emitted when no opencode auth
	// source can be detected. Defaults to os.Stderr when nil.
	WarnWriter io.Writer
}

type adapter struct {
	lookPath agents.LookPathFunc

	// hookBin is the absolute path to the springfield binary invoked by the
	// guard plugin installed under <workdir>/.springfield/opencode/.
	hookBin string

	warnBuf io.Writer

	// authWarnOnce guards the one-time "no stored opencode auth / no
	// provider key" warning.
	authWarnOnce sync.Once
}

// The runtime discovers capabilities by optional type assertion, so a dropped
// method would silently disable the capability rather than fail a build. Pin
// what exists after this section; later sections add their own pins.
var (
	_ agents.Commander         = (*adapter)(nil)
	_ agents.ResultValidator   = (*adapter)(nil)
	_ agents.TranscriptDecoder = (*adapter)(nil)
	_ agents.ErrorClassifier   = (*adapter)(nil)
	_ agents.ModelProvider     = (*adapter)(nil)
)

// New constructs an opencode adapter with default options. Returns an
// agents.Commander so the runtime can build runnable commands.
func New(lookPath agents.LookPathFunc) agents.Commander {
	return NewWithOptions(lookPath, Options{})
}

// NewWithOptions constructs an opencode adapter with optional behaviour
// overrides (e.g. injecting a custom warn writer for tests).
func NewWithOptions(lookPath agents.LookPathFunc, opts Options) agents.Commander {
	if lookPath == nil {
		lookPath = osexec.LookPath
	}

	hookBin, err := os.Executable()
	if err != nil || hookBin == "" {
		// Fallback: trust PATH at hook-run time. Non-fatal — matches the
		// Claude/Gemini adapters' posture.
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
	return agents.AgentOpenCode
}

// SuggestedModels delegates to the package-level curated list; see models.go.
func (a *adapter) SuggestedModels() []string {
	return SuggestedModels()
}

func (a *adapter) Metadata() agents.Metadata {
	return agents.Metadata{
		ID:           agents.AgentOpenCode,
		Name:         "OpenCode",
		Binary:       "opencode",
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

// Command builds a runnable opencode invocation.
//
// IMPORTANT: the prompt rides Stdin, never argv — opencode's non-interactive
// mode reads piped stdin and appends it to the message, so a 256 KB context_md
// payload cannot hit ARG_MAX. No -c/-s/--fork resume flags: Springfield runs a
// fresh process per iteration by design.
func (a *adapter) Command(input agents.CommandInput) (coreexec.Command, error) {
	args := []string{"run", "--format", "json", "--auto"}
	if m := strings.TrimSpace(input.ExecutionSettings.OpenCode.Model); m != "" {
		args = append(args, "--model", m)
	}

	env, err := a.commandEnv(input)
	if err != nil {
		// Fail closed — never spawn opencode without the guard plugin.
		return coreexec.Command{}, err
	}

	a.maybeEmitAuthWarning()

	return coreexec.Command{
		Name:  "opencode",
		Args:  args,
		Stdin: input.Prompt,
		Dir:   input.WorkDir,
		Env:   env,
	}, nil
}

// Positive-signal contract: ValidateResult returns nil only when the
// transcript carries an explicit success marker — at least one tool_use event
// whose part.state.status reports "completed". Text-only runs, all-tools-
// errored runs, and non-zero exits all fail validation.
//
// Field shapes below are CONFIRMED against real captures (opencode CLI
// v1.18.21, free default model, 2026-08-22; fixtures-of-record under
// tests/realcaptures/opencode/):
//
//   - stdout is NDJSON, one JSON object per line:
//     {"type","timestamp","sessionID","part"}.
//   - Dual tagging: outer snake_case "type" ("step_start", "step_finish",
//     "tool_use", "text", "error") diverges from part.type camelCase
//     ("step-start", "step-finish", "tool", "text"). Decode the OUTER type.
//   - A tool call and its result are ONE unified event (no paired
//     call/result events): part.tool names the tool, part.state.status is
//     "completed" or "error" (both observed in captures), part.state.input /
//     part.state.output carry payload.
//   - Tool arg key names in part.state.input (guard-translation contract,
//     pinned by tests/agents/opencode_guard_args_test.go):
//     write → filePath,content; edit → filePath,oldString,newString;
//     bash → command; read → filePath.
//   - Final assistant text lives at part.text on type:"text" events;
//     part.time.end is set once the text is finalized.
//   - step_finish.part.reason: "tool-calls" (mid-run) or "stop" (final);
//     there is no dedicated done event. Secondary signal only.
//   - Hard failure emits a top-level error event ON STDOUT (stderr empty)
//     with exit code 1:
//     {"type":"error",...,"error":{"name":"UnknownError","data":{"message":...,"ref":...}}}.
//   - Every step_finish carries part.cost (provider-computed USD) and
//     part.tokens {total,input,output,reasoning,cache:{write,read}}; cost
//     read 0 on the free-tier model used for capture (non-zero dollar
//     values unverified).
//   - No reasoning or task/subagent events appeared in any capture.
func (a *adapter) ValidateResult(result coreexec.Result, requireToolAction bool) error {
	if result.ExitCode != 0 {
		if msg := lastErrorMessage(result.Events); msg != "" {
			return fmt.Errorf("opencode exited with non-zero code %d: %s", result.ExitCode, msg)
		}
		return fmt.Errorf("opencode exited with non-zero code %d", result.ExitCode)
	}

	// Tool-free reviewer: past the exit-code guard, a clean exit is a valid
	// completion even with no tool work; the verdict scanner judges substance.
	if !requireToolAction {
		return nil
	}

	sawCompletedToolUse := false
	for _, e := range result.Events {
		if e.Type != coreexec.EventStdout {
			continue
		}
		var ev opencodeStreamEvent
		if err := json.Unmarshal([]byte(e.Data), &ev); err != nil {
			continue
		}
		if ev.Type == "tool_use" && ev.Part.State.Status == "completed" {
			sawCompletedToolUse = true
		}
	}

	if sawCompletedToolUse {
		return nil
	}
	return errors.New("opencode exited without completing tool work")
}

// opencodeStreamEvent mirrors only the fields the transcript parsers consume.
// The outer "type" is authoritative (see ValidateResult doc comment); part
// fields are addressed through Part.
type opencodeStreamEvent struct {
	Type string              `json:"type"`
	Part opencodeMessagePart `json:"part"`
	Err  *struct {
		Name string `json:"name"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	} `json:"error"`
}

type opencodeMessagePart struct {
	Type string `json:"type"`
	Tool string `json:"tool"`
	Text string `json:"text"`
	Time *struct {
		Start int64 `json:"start"`
		End   int64 `json:"end"`
	} `json:"time"`
	State struct {
		Status string          `json:"status"`
		Input  json.RawMessage `json:"input"`
		Output json.RawMessage `json:"output"`
	} `json:"state"`
}

// lastErrorMessage returns the message of the LAST decoded top-level error
// event in the stream (opencode emits one terminal error line on failure).
func lastErrorMessage(events []coreexec.Event) string {
	msg := ""
	for _, e := range events {
		if e.Type != coreexec.EventStdout {
			continue
		}
		var ev opencodeStreamEvent
		if err := json.Unmarshal([]byte(e.Data), &ev); err != nil {
			continue
		}
		if ev.Type == "error" && ev.Err != nil && ev.Err.Data.Message != "" {
			msg = ev.Err.Data.Message
		}
	}
	return msg
}

// AssistantText decodes the reviewer's plain text out of opencode's NDJSON
// stream so the review gate scans real newlines, not the raw JSON transport
// (BUG-1). Joins part.text from type:"text" events whose time.end is set —
// unfinalized streaming dregs are excluded (see ValidateResult doc comment
// for the confirmed field shapes).
func (a *adapter) AssistantText(events []coreexec.Event) string {
	var parts []string
	for _, e := range events {
		if e.Type != coreexec.EventStdout {
			continue
		}
		var ev opencodeStreamEvent
		if err := json.Unmarshal([]byte(e.Data), &ev); err != nil {
			continue
		}
		if ev.Type != "text" || ev.Part.Text == "" {
			continue
		}
		if ev.Part.Time == nil || ev.Part.Time.End == 0 {
			continue
		}
		parts = append(parts, ev.Part.Text)
	}
	return strings.Join(parts, "\n")
}

// ClassifyError normalizes a failed opencode run into retryable-vs-fatal so
// the runtime can fall back to the next agent in priority. Mirrors the gemini
// adapter's precedence exactly: a clean exit is fatal FIRST (a validator
// rejection is a real verdict, not a transport blip — no needle can rescue
// it), then binary-not-found is retryable (install-time problem, not
// plan-fatal), then the needle list scans process error text, stderr lines,
// and decoded top-level error-event messages for transient-failure needles.
func (a *adapter) ClassifyError(events []coreexec.Event, exitCode int, err error) agents.ErrorClass {
	if exitCode == 0 {
		return agents.ErrorClassFatal
	}
	if errors.Is(err, osexec.ErrNotFound) {
		return agents.ErrorClassRetryable
	}
	if opencodeRetryableText(errorString(err)) {
		return agents.ErrorClassRetryable
	}
	for _, event := range events {
		if opencodeRetryableEvent(event) {
			return agents.ErrorClassRetryable
		}
	}
	return agents.ErrorClassFatal
}

var opencodeRetryableNeedles = []string{
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

func opencodeRetryableText(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return false
	}
	for _, needle := range opencodeRetryableNeedles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// opencodeRetryableEvent scans stderr verbatim; stdout lines must decode as a
// top-level error event before their message is needle-scanned (plain model
// chatter and tool output on stdout never trip the list).
func opencodeRetryableEvent(event coreexec.Event) bool {
	if event.Type == coreexec.EventStderr {
		return opencodeRetryableText(event.Data)
	}
	if event.Type != coreexec.EventStdout {
		return false
	}

	var ev opencodeStreamEvent
	if err := json.Unmarshal([]byte(event.Data), &ev); err != nil {
		return false
	}
	if ev.Type != "error" || ev.Err == nil {
		return false
	}

	return opencodeRetryableText(ev.Err.Data.Message)
}

// maybeEmitAuthWarning warns once (per adapter instance) when no opencode auth
// source is detectable. Non-blocking — the subprocess still runs and may pick
// up auth we couldn't see (e.g. a provider plugin reading its own env).
func (a *adapter) maybeEmitAuthWarning() {
	for _, key := range opencodeProviderEnvKeys {
		if os.Getenv(key) != "" {
			return
		}
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if _, statErr := os.Stat(filepath.Join(home, ".local", "share", "opencode", "auth.json")); statErr == nil {
			return
		}
	}
	a.authWarnOnce.Do(func() {
		fmt.Fprintln(a.warnBuf,
			"springfield: no provider API key env vars set and no stored opencode auth at ~/.local/share/opencode/auth.json — subprocess may fail to reach a model",
		)
	})
}

// opencodeProviderEnvKeys are the obvious provider key variables opencode's
// model providers read directly from the environment.
var opencodeProviderEnvKeys = []string{
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
	"GEMINI_API_KEY",
	"GOOGLE_API_KEY",
	"XAI_API_KEY",
	"OPENROUTER_API_KEY",
	"GROQ_API_KEY",
	"MISTRAL_API_KEY",
	"AZURE_OPENAI_API_KEY",
	"GITHUB_TOKEN",
	"GH_TOKEN",
}
