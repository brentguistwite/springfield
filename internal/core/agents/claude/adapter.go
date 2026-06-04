package claude

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
	"time"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
)

// Options configures optional adapter behaviour. Zero value is valid and uses
// sensible defaults (os.Stderr for warning output).
type Options struct {
	// WarnWriter receives the one-time warning emitted when settings.json is
	// unreadable. Defaults to os.Stderr when nil.
	WarnWriter io.Writer
}

type adapter struct {
	lookPath agents.LookPathFunc
	// hookBin is the absolute path to the springfield binary used by the
	// PreToolUse hook. Resolved at construction via os.Executable() so the
	// hook always invokes the same binary the user launched, regardless of
	// PATH shuffles in child processes. If resolution fails, falls back to
	// the bare name "springfield" for PATH lookup at hook time.
	hookBin string

	// warnBuf is the writer for the one-time warning about unreadable
	// settings.json. Defaults to os.Stderr. Overridable for tests.
	warnBuf io.Writer

	// warnOnce guards the one-time warning about unreadable settings.json.
	// Lives on the struct (not package-level) so each freshly constructed
	// adapter instance fires its warning independently — required for
	// deterministic test assertions under go test parallelism.
	warnOnce sync.Once
}

// New constructs a claude adapter with default options.
func New(lookPath agents.LookPathFunc) agents.Commander {
	return NewWithOptions(lookPath, Options{})
}

// NewWithOptions constructs a claude adapter, allowing optional configuration
// (e.g. injecting a custom warn writer for tests).
func NewWithOptions(lookPath agents.LookPathFunc, opts Options) agents.Commander {
	if lookPath == nil {
		lookPath = osexec.LookPath
	}

	hookBin, err := os.Executable()
	if err != nil || hookBin == "" {
		// Fallback: trust PATH at hook-run time. Non-fatal.
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
	return agents.AgentClaude
}

func (a *adapter) Metadata() agents.Metadata {
	return agents.Metadata{
		ID:           agents.AgentClaude,
		Name:         "Claude Code",
		Binary:       "claude",
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

func (a *adapter) Command(input agents.CommandInput) (coreexec.Command, error) {
	// -p enables non-interactive print mode; prompt is delivered via stdin
	// rather than as a positional arg so it is not visible in `ps aux`.
	// --output-format and --verbose only work with -p.
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
	}
	if permissionMode := strings.TrimSpace(input.ExecutionSettings.Claude.PermissionMode); permissionMode != "" {
		args = append(args, "--permission-mode", permissionMode)
	}
	if model := strings.TrimSpace(input.ExecutionSettings.Claude.Model); model != "" {
		args = append(args, "--model", model)
	}

	// Hard-block agent writes to Springfield's control plane with a
	// PreToolUse hook. The hook command invokes `springfield hook-guard`,
	// which inspects path-bearing fields of the tool_input JSON on stdin
	// and exits 2 when any of them target .springfield/. Path-aware (vs.
	// substring grep) so legitimate edits whose *content* merely mentions
	// .springfield are allowed through.
	args = append(args, "--settings", a.springfieldControlPlaneSettingsJSON())

	return coreexec.Command{
		Name:  "claude",
		Args:  args,
		Stdin: input.Prompt,
		Dir:   input.WorkDir,
	}, nil
}

func (a *adapter) SuggestedModels() []string {
	return SuggestedModels()
}

func (a *adapter) ClassifyError(events []coreexec.Event, exitCode int, err error) agents.ErrorClass {
	if errors.Is(err, osexec.ErrNotFound) {
		return agents.ErrorClassRetryable
	}
	if claudeRetryableText(errorString(err)) {
		return agents.ErrorClassRetryable
	}
	for _, event := range events {
		if claudeRetryableEvent(event) {
			return agents.ErrorClassRetryable
		}
	}
	// Clean-exit fatal bail-out runs LAST, after the retryable scans.
	// ValidateResult synthesizes an error on a clean (exitCode 0) run when
	// the transcript lacks a paired tool_result — the truncation pattern that
	// rate-limits and API errors produce. Scanning the events first lets that
	// synthesized error classify retryable so the agent_priority fallback can
	// fire, while a genuinely clean exit with no retryable signal stays fatal.
	if exitCode == 0 {
		return agents.ErrorClassFatal
	}
	return agents.ErrorClassFatal
}

// Cooldown extracts the claude rate-limit reset timestamp from a failed
// run. Returns the zero time when no parseable reset message is present.
// now is supplied by the runner so wall-clock-format messages resolve
// against the same reference time used for skip decisions.
func (a *adapter) Cooldown(events []coreexec.Event, exitCode int, err error, now time.Time) time.Time {
	return parseCooldown(events, exitCode, err, now)
}

// SpringfieldControlPlaneHookCommand returns the hook command string used
// in the --settings JSON. Exposed as an instance method because the command
// embeds the resolved springfield binary path (see adapter.hookBin).
func (a *adapter) SpringfieldControlPlaneHookCommand() string {
	// Quote the binary path so paths with spaces survive shell parsing.
	// The hook-guard subcommand never touches the shell itself; the quoting
	// matters for Claude's shell-based hook runner.
	// --block-reentry: this hook guards a Springfield-spawned subagent, which
	// must not re-enter any springfield start/plan/recover (see hook-guard).
	return shellQuote(a.hookBin) + " hook-guard --block-reentry"
}

// shellQuote wraps s in single quotes, escaping any embedded single quotes.
// Used for the PreToolUse hook command string so paths with spaces/quotes
// survive the shell layer.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// springfieldControlPlaneSettingsJSON returns the inline --settings payload
// registering the PreToolUse hook that protects .springfield/ from agent
// writes, and plugin disables that prevent subagent recursion via the
// springfield and superpowers plugins.
func (a *adapter) springfieldControlPlaneSettingsJSON() string {
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
		"permissions": map[string]any{
			"deny": subagentDeniedTools(),
		},
	}

	pluginDisables := a.resolveSubagentPluginDisables()
	if len(pluginDisables) > 0 {
		payload["enabledPlugins"] = pluginDisables
	}

	data, err := json.Marshal(payload)
	if err != nil {
		// payload contains only strings, []string, and a string-keyed map —
		// json.Marshal cannot fail on these types. Fail LOUD if that invariant
		// is ever broken: a hand-built fallback risks invalid JSON (hookCommand
		// is shell-quoted, not JSON-escaped) or silently dropping the deny list
		// / plugin-disables, downgrading the subagent to a full-surface,
		// plugin-enabled state — worse than crashing.
		panic(fmt.Sprintf("springfield: control-plane settings marshal failed (unexpected): %v", err))
	}
	return string(data)
}

// subagentDeniedTools returns the built-in parent-harness primitives that a
// Springfield-managed subagent must NOT inherit. They are emitted under
// permissions.deny in the --settings payload.
//
// Why a deny list (not a positive allowlist): in Claude Code settings.json,
// permissions.allow only PRE-APPROVES a tool (skips the prompt) — it does not
// remove unlisted tools. permissions.deny is the only settings.json mechanism
// that actually strips a tool from the subagent's surface (`--disallowedTools`
// is the CLI-flag equivalent; there is no top-level disallowedTools key in
// settings.json). A deny list also leaves project-configured MCP tools
// (mcp__*) and the implementer tool surface (Bash, Edit, Glob, Grep,
// MultiEdit, NotebookEdit, Read, Skill, ToolSearch, Write) untouched for free
// — a positive allowlist would have to enumerate unknowable MCP tool names.
//
// These primitives are no-op footguns inside a managed subagent: the dogfood
// plan-04 agent actually invoked ScheduleWakeup from within one, burning a
// turn on a tool that can never fire in this context.
func subagentDeniedTools() []string {
	return []string{
		// Subagent spawning / orchestration — prevents recursion.
		"Task",
		"TaskCreate", "TaskGet", "TaskList", "TaskOutput", "TaskStop", "TaskUpdate",
		"Workflow",
		// Scheduling / background wakeups — meaningless in a one-shot run.
		"ScheduleWakeup",
		"CronCreate", "CronDelete", "CronList",
		"Monitor",
		// Network reach beyond the agent CLIs' own calls.
		"WebFetch", "WebSearch",
		// Outbound notification / remote-trigger / messaging surfaces.
		"PushNotification",
		"RemoteTrigger",
		"SendMessage",
		// Team management.
		"TeamCreate", "TeamDelete",
		// Plan / worktree mode switches owned by the parent harness.
		"EnterPlanMode", "ExitPlanMode",
		"EnterWorktree", "ExitWorktree",
	}
}

// resolveSubagentPluginDisables reads ~/.claude/settings.json at Command time
// (NOT at New time) and returns a map of plugin IDs to disable (false) in the
// subagent's --settings JSON.
//
// Three cases:
//  1. settings readable + plugin key present → emit {<id>: false} for each
//     matched springfield@* / superpowers@* key
//  2. settings readable but no matching key → empty map (no-op)
//  3. settings unreadable → emit warning once per adapter instance, return
//     hardcoded defaults (springfield@brentguistwite,
//     superpowers@claude-plugins-official)
func (a *adapter) resolveSubagentPluginDisables() map[string]bool {
	home, err := os.UserHomeDir()
	if err != nil {
		a.emitSettingsWarning(fmt.Sprintf("os.UserHomeDir: %v", err))
		return defaultPluginDisables()
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		a.emitSettingsWarning(err.Error())
		return defaultPluginDisables()
	}

	var settings struct {
		EnabledPlugins map[string]any `json:"enabledPlugins"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		a.emitSettingsWarning(fmt.Sprintf("JSON parse error: %v", err))
		return defaultPluginDisables()
	}

	result := make(map[string]bool)
	for id := range settings.EnabledPlugins {
		if isTargetPlugin(id) {
			result[id] = false
		}
	}
	return result
}

// isTargetPlugin reports whether a plugin ID matches the springfield@* or
// superpowers@* prefix patterns that should be disabled in subagents.
func isTargetPlugin(id string) bool {
	return strings.HasPrefix(id, "springfield@") || strings.HasPrefix(id, "superpowers@")
}

// defaultPluginDisables returns the hardcoded fallback disable map used when
// settings.json is unreadable.
func defaultPluginDisables() map[string]bool {
	return map[string]bool{
		"springfield@brentguistwite":          false,
		"superpowers@claude-plugins-official": false,
	}
}

// emitSettingsWarning emits the one-time warning about unreadable settings.json.
// Uses sync.Once on the adapter struct so each adapter instance emits at most
// one warning.
func (a *adapter) emitSettingsWarning(errMsg string) {
	a.warnOnce.Do(func() {
		fmt.Fprintf(a.warnBuf,
			"springfield: cannot read ~/.claude/settings.json: %s — applying default plugin-disable IDs; subagent may still see plugin if installed under a different marketplace slug\n",
			errMsg,
		)
	})
}

// Positive-signal contract: ValidateResult returns nil only when the
// transcript carries an explicit success marker — at least one tool_use ID
// emitted by the assistant whose paired tool_result reports is_error == false,
// or a top-level result event with subtype == "success" and is_error ==
// false. Absence of failure markers is not enough; refusal-with-no-tools and
// all-tools-errored runs both fail validation. Process-level failures
// (non-zero exit, hard error) also fail before stream inspection.
func (a *adapter) ValidateResult(result coreexec.Result, requireToolAction bool) error {
	if result.ExitCode != 0 {
		return fmt.Errorf("claude exited with non-zero code %d", result.ExitCode)
	}

	seenToolUseIDs := map[string]bool{}
	sawSuccessfulToolResult := false
	sawSuccessResult := false

	for _, e := range result.Events {
		if e.Type != coreexec.EventStdout {
			continue
		}
		var event claudeStreamEvent
		if err := json.Unmarshal([]byte(e.Data), &event); err != nil {
			continue
		}
		if event.Type == "result" && event.Subtype == "success" && !event.IsError {
			sawSuccessResult = true
			continue
		}
		for _, item := range event.Message.Content {
			switch item.Type {
			case "tool_use":
				if item.ID != "" {
					seenToolUseIDs[item.ID] = true
				}
			case "tool_result":
				if item.IsError {
					continue
				}
				if item.ToolUseID != "" && seenToolUseIDs[item.ToolUseID] {
					sawSuccessfulToolResult = true
				}
			}
		}
	}

	if sawSuccessfulToolResult {
		return nil
	}
	if !requireToolAction && sawSuccessResult {
		// Tool-free reviewer: a terminal result event with subtype "success"
		// and is_error == false is a valid completion even with zero tool
		// calls. The verdict scanner judges substance; this only confirms the
		// process completed cleanly.
		return nil
	}
	if sawSuccessResult && len(seenToolUseIDs) == 0 {
		// No tool work attempted; a success-typed result alone is not
		// a positive completion signal under the implementer contract.
		return errors.New("claude exited without taking action")
	}
	if len(seenToolUseIDs) == 0 {
		return errors.New("claude exited without invoking any tool")
	}
	return errors.New("claude exited without a successful tool_result")
}

type claudeStreamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Message struct {
		Content []claudeMessageContent `json:"content"`
	} `json:"message"`
}

type claudeMessageContent struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	ToolUseID string `json:"tool_use_id"`
	IsError   bool   `json:"is_error"`
	Content   any    `json:"content"`
}

var claudeRetryableNeedles = []string{
	"rate limit",
	"rate-limit",
	"rate_limit",
	// "usage limit" is claude-code's canonical rate-limit diagnostic
	// ("Claude AI usage limit reached|<epoch>"). It surfaces on stderr/err,
	// and cooldown.go's reRateLimitPhrase already treats it as a rate-limit
	// phrase — classification must agree so the parsed reset actually gets
	// installed instead of bailing Fatal. Kept OUT of the narrow stdout list
	// (claudeRetryableStdoutNeedles) on purpose: that list trips on tool_result
	// content too, and "usage limit" is generic enough to false-positive there.
	"usage limit",
	"api_error_status",
	"too many requests",
	"429",
	"quota exceeded",
	"resource exhausted",
	"overloaded_error",
	"authentication_error",
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
	// Springfield-synthesized turn-cap trip from [coreruntime.EnforceTurnCap].
	// When the runtime layer demotes a clean exit to retryable because the
	// iteration burned more turns than the cap (the dogfood thrash signal),
	// the error string carries this prefix. Treating it as retryable lets
	// the agent_priority fallback walk to codex/gemini instead of bailing
	// the whole iteration on the over-cap claude run. The match is hand-
	// duplicated (rather than importing the constant) to keep the claude
	// adapter free of an inverse dependency on the runtime package.
	"iteration-turn-cap-exceeded",
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// claudeRetryableStdoutNeedles is the NARROW list scanned against stdout.
// stdout carries two unrelated kinds of text under --output-format stream-json:
// claude-code's own structured API signals (rate_limit_event, api_error_status,
// overloaded_error) AND the verbatim content of tool_result events — i.e. the
// output of whatever tool the agent ran. Bare numeric/phrase needles like "500"
// or "service unavailable" are meaningful on stderr (claude-code's diagnostics)
// but would falsely match app-level errors echoed inside tool_result content
// (e.g. an app that "returned HTTP 500"). Those are the agent's task failures,
// not upstream Anthropic issues, and must stay Fatal — so stdout only trips on
// the explicit structured-signal fields below.
//
// Entries here are STRUCTURED stream-json field names ONLY, never generic
// English phrases. "rate_limit_event" is safe because it is a literal JSON key
// the claude-code stream emits; "rate limit" / "rate-limit" / "usage limit"
// would also match a tool_result whose body happens to mention rate limiting
// (e.g. an app under test logging "rate-limit exceeded"). Those plain-text
// phrases live in [claudeRetryableNeedles] (stderr) where the source is always
// claude-code's own diagnostics.
var claudeRetryableStdoutNeedles = []string{
	"rate_limit_event",
	// "rate_limit" (underscore) intentionally removed: it false-positives on
	// app tool_result content like `{"error":"rate_limit_exceeded"}`. The
	// "rate_limit_event" entry above already structurally covers Claude's
	// actual rate-limit stream event (Contains-style match catches it
	// regardless of surrounding JSON wrapping).
	"api_error_status",
	"overloaded_error",
}

func containsRetryableNeedle(s string, needles []string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return false
	}
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func claudeRetryableText(s string) bool {
	return containsRetryableNeedle(s, claudeRetryableNeedles)
}

// claudeRetryableEvent scans a single output event for retryable signals,
// stream-aware: stdout is matched against the narrow structured-signal list
// (claudeRetryableStdoutNeedles) so app-level tool_result errors don't false-
// positive, while stderr — claude-code's own diagnostics — uses the full
// needle list. Dropping the prior stderr-only filter lets the structured
// rate_limit_event / api_error_status payloads that stream-json emits on
// stdout become visible to the retryable scan.
//
// The dispatch is explicit per event type — any non-stdout / non-stderr event
// (system / meta / future event kinds) does NOT get scanned, so a future
// EventType added to coreexec cannot accidentally start matching the full
// needle list and produce false-positive retries.
func claudeRetryableEvent(event coreexec.Event) bool {
	switch event.Type {
	case coreexec.EventStdout:
		return containsRetryableNeedle(event.Data, claudeRetryableStdoutNeedles)
	case coreexec.EventStderr:
		return claudeRetryableText(event.Data)
	default:
		return false
	}
}
