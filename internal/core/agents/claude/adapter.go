package claude

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
		lookPath = exec.LookPath
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
	case errors.Is(err, exec.ErrNotFound):
		result.Status = agents.DetectionStatusMissing
	default:
		result.Status = agents.DetectionStatusUnhealthy
	}

	return result
}

func (a *adapter) Command(input agents.CommandInput) coreexec.Command {
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
	}
}

// SpringfieldControlPlaneHookCommand returns the hook command string used
// in the --settings JSON. Exposed as an instance method because the command
// embeds the resolved springfield binary path (see adapter.hookBin).
func (a *adapter) SpringfieldControlPlaneHookCommand() string {
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
	}

	pluginDisables := a.resolveSubagentPluginDisables()
	if len(pluginDisables) > 0 {
		payload["enabledPlugins"] = pluginDisables
	}

	data, err := json.Marshal(payload)
	if err != nil {
		// payload is static — marshal errors are impossible in practice,
		// but fall back to a hand-built string rather than panic.
		return `{"hooks":{"PreToolUse":[{"matcher":"Write|Edit|MultiEdit|NotebookEdit|Bash","hooks":[{"type":"command","command":"` + hookCommand + `"}]}]}}`
	}
	return string(data)
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
		"springfield@brentguistwite":       false,
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

// ValidateResult checks Claude's stream-json output for rejected tool calls,
// which indicate the agent couldn't complete the task autonomously.
func (a *adapter) ValidateResult(result coreexec.Result) error {
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
