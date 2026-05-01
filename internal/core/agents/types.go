package agents

import (
	"context"
	"errors"

	"springfield/internal/core/exec"
)

type ID string

const (
	AgentClaude ID = "claude"
	AgentCodex  ID = "codex"
	AgentGemini ID = "gemini"
)

type Capability string

type CapabilitySet map[Capability]bool

type Metadata struct {
	ID           ID
	Name         string
	Binary       string
	Capabilities CapabilitySet
}

type DetectionStatus string

const (
	DetectionStatusAvailable DetectionStatus = "available"
	DetectionStatusMissing   DetectionStatus = "missing"
	DetectionStatusUnhealthy DetectionStatus = "unhealthy"
)

type Detection struct {
	ID     ID
	Name   string
	Binary string
	Status DetectionStatus
	Path   string
	Err    error
}

type Adapter interface {
	ID() ID
	Metadata() Metadata
	Detect(context.Context) Detection
}

type LookPathFunc func(string) (string, error)

type ResolveInput struct {
	ProjectDefault ID
	PlanOverride   ID
}

type ResolveSource string

const (
	ResolveSourceProjectDefault ResolveSource = "project_default"
	ResolveSourcePlanOverride   ResolveSource = "plan_override"
)

type Resolved struct {
	Adapter Adapter
	Source  ResolveSource
}

// ExecutionSettings carries adapter-specific execution configuration across the
// runtime boundary.
type ExecutionSettings struct {
	Claude ClaudeExecutionSettings
	Codex  CodexExecutionSettings
	Gemini GeminiExecutionSettings
}

type ClaudeExecutionSettings struct {
	Model          string
	PermissionMode string
}

type CodexExecutionSettings struct {
	SandboxMode    string
	ApprovalPolicy string
}

// GeminiExecutionSettings carries Gemini CLI execution settings.
//
//   - ApprovalMode maps to --approval-mode (default|auto_edit|yolo|plan).
//   - SandboxMode maps to --sandbox (empty to omit; true|docker|podman|
//     sandbox-exec|runsc|lxc per Gemini CLI docs).
//   - Model maps to --model (empty delegates to Gemini's default; alias
//     pro|flash|flash-lite|auto or a concrete model ID also valid).
type GeminiExecutionSettings struct {
	ApprovalMode string
	SandboxMode  string
	Model        string
}

// CommandInput provides the parameters needed to build an agent CLI invocation.
type CommandInput struct {
	Prompt            string
	WorkDir           string
	ExecutionSettings ExecutionSettings
}

// Commander extends Adapter with the ability to produce a runnable command spec.
//
// Returns a non-nil error when the adapter cannot safely build a runnable
// command (e.g. Gemini refuses to spawn when its control-plane hook file
// can't be written). The runtime surfaces this as a failed run rather than
// executing an unprotected subprocess.
type Commander interface {
	Adapter
	Command(input CommandInput) (exec.Command, error)
}

// ResultValidator optionally validates whether exit code 0 truly means task success.
// Adapters that implement this are checked by the runner after a clean exit.
type ResultValidator interface {
	// Positive-signal contract: ValidateResult returns nil only when the
	// agent's transcript carries an explicit positive completion signal
	// (e.g. a tool_use/tool_result success pair, or an adapter-specific
	// item.completed work event). Absence of failure markers is not
	// enough; refusal-with-no-tools, all-tools-errored, and text-only
	// runs all return a non-nil, operator-readable error. Adapter-
	// specific OS exit-code rules (Gemini exit 53/42) and process-level
	// failures take precedence over stream inspection.
	ValidateResult(result exec.Result) error
}

// ErrorClass classifies whether a runtime failure is worth falling back to the
// next agent in priority. Adapters parse provider-specific stderr/exit codes
// and normalize into this enum; runtime sees only the enum.
type ErrorClass string

const (
	// ErrorClassRetryable: try the next agent in priority. Covers rate limit,
	// quota exceeded, transient API 5xx, CLI auth expired, CLI not found,
	// network failure.
	ErrorClassRetryable ErrorClass = "retryable"
	// ErrorClassFatal: bubble up immediately. Covers user-fault errors
	// (bad input, validator rejection, plan syntax error).
	ErrorClassFatal ErrorClass = "fatal"
)

// ErrorClassifier optionally classifies a failed run. Adapters that implement
// this are consulted by the runtime when an agent's run fails; the result
// determines whether fallback proceeds. Adapters without this interface
// default to ErrorClassFatal (no automatic fallback).
type ErrorClassifier interface {
	ClassifyError(events []exec.Event, exitCode int, err error) ErrorClass
}

// ModelProvider exposes a small curated list of "blessed/tested" models for an
// adapter. Used by the init picker as autocomplete suggestions; free-text
// input remains the primary path so new models are always reachable.
type ModelProvider interface {
	SuggestedModels() []string
}

var ErrUnsupportedAgent = errors.New("unsupported agent")
