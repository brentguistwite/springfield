package config

import (
	"strings"

	"springfield/internal/core/agents"
)

type ExecutionMode string

const (
	ExecutionModeRecommended ExecutionMode = "recommended"
	ExecutionModeOff         ExecutionMode = "off"
	ExecutionModeCustom      ExecutionMode = "custom"
)

type AgentExecutionModes struct {
	Claude ExecutionMode
	Codex  ExecutionMode
	Gemini ExecutionMode
}

// Config is the shared project configuration loaded from springfield.toml.
type Config struct {
	Project ProjectConfig         `toml:"project"`
	Agents  AgentsConfig          `toml:"agents"`
	Plans   map[string]PlanConfig `toml:"plans"`
	Start   StartConfig           `toml:"start"`
}

// StartConfig holds settings for the start command.
type StartConfig struct {
	// KeepAwake nil means default (true); false opts out of sleep prevention.
	KeepAwake *bool `toml:"keep_awake,omitempty"`
}

// KeepAwakeEnabled reports whether sleep prevention is active.
// Defaults to true; set keep_awake = false in [start] to disable.
func (c Config) KeepAwakeEnabled() bool {
	if c.Start.KeepAwake == nil {
		return true
	}
	return *c.Start.KeepAwake
}

// AgentForPlan resolves the effective agent for a plan. Returns the per-plan
// override if set; otherwise priority[0]. Returns "" when neither is set.
func (c Config) AgentForPlan(planID string) string {
	if plan, ok := c.Plans[planID]; ok && plan.Agent != "" {
		return plan.Agent
	}
	if len(c.Project.AgentPriority) > 0 {
		return c.Project.AgentPriority[0]
	}
	return ""
}

// ProjectConfig stores project-wide defaults.
type ProjectConfig struct {
	AgentPriority []string `toml:"agent_priority,omitempty"`
	// AllowProtectedBase opts out of the protected-base preflight guard.
	// Only honored when AutoBranch is explicitly false. With auto-branching
	// enabled (the default) Springfield never lands on a protected base in
	// the first place because it auto-cuts a feature branch before the run.
	AllowProtectedBase bool `toml:"allow_protected_base,omitempty"`
	// AutoBranch controls whether `springfield start` auto-cuts a feature
	// branch when the operator is on a protected base ("main"/"master").
	// Pointer so default-true is distinguishable from explicit false.
	// nil → enabled. Set false to fall back to the refuse-or-allow guard.
	AutoBranch *bool `toml:"auto_branch,omitempty"`
	// AutoBranchPattern is the template Springfield renders into the
	// auto-cut branch name. Supported placeholder: {id} (batch ID).
	// Empty → defaults to "springfield/batch-{id}".
	AutoBranchPattern string `toml:"auto_branch_pattern,omitempty"`
	// MaxTurnsPerIteration caps how many agent turns a single plan iteration
	// may consume before Springfield synthesizes an
	// 'iteration-turn-cap-exceeded' failure — the early circuit-breaker for the
	// 84-turn thrash observed in dogfooding. Pointer so an omitted key
	// (nil → DefaultMaxTurnsPerIteration) is distinguishable from an explicit
	// 0 (cap disabled). Resolve the effective value via MaxTurnsPerIteration().
	MaxTurnsPerIteration *int `toml:"max_turns_per_iteration,omitempty"`
}

// DefaultMaxTurnsPerIteration is the per-iteration agent-turn ceiling applied
// when [project] max_turns_per_iteration is omitted from springfield.toml.
const DefaultMaxTurnsPerIteration = 40

// DefaultAutoBranchPattern is the branch-name template used when the project
// does not configure auto_branch_pattern.
const DefaultAutoBranchPattern = "springfield/batch-{id}"

// AutoBranchEnabled reports whether auto-branching is active. Default true:
// only an explicit auto_branch = false in springfield.toml disables it.
func (c Config) AutoBranchEnabled() bool {
	if c.Project.AutoBranch == nil {
		return true
	}
	return *c.Project.AutoBranch
}

// AutoBranchPatternOrDefault returns the configured branch-name pattern, or
// [DefaultAutoBranchPattern] when unset.
func (c Config) AutoBranchPatternOrDefault() string {
	if strings.TrimSpace(c.Project.AutoBranchPattern) == "" {
		return DefaultAutoBranchPattern
	}
	return c.Project.AutoBranchPattern
}

// MaxTurnsPerIteration resolves the effective per-iteration agent-turn cap:
//
//   - omitted (nil)       → [DefaultMaxTurnsPerIteration] (40)
//   - explicit 0          → 0 (cap disabled)
//   - explicit negative   → 0 (cap disabled; negative is treated as "off")
//   - explicit positive n → n
//
// A return value of 0 means "no cap" and callers skip enforcement entirely.
// Note the deliberate asymmetry: an *omitted* key defaults to 40, but an
// *explicit* 0 disables the cap — a pointer field is what lets us tell the two
// apart.
func (c Config) MaxTurnsPerIteration() int {
	v := c.Project.MaxTurnsPerIteration
	if v == nil {
		return DefaultMaxTurnsPerIteration
	}
	if *v <= 0 {
		return 0
	}
	return *v
}

// ExecutionSettingsForAgent resolves adapter-specific execution settings for
// the requested agent id.
func (c Config) ExecutionSettingsForAgent(agentID string) agents.ExecutionSettings {
	settings := c.ExecutionSettings()
	switch agentID {
	case string(agents.AgentClaude):
		return agents.ExecutionSettings{
			Claude: settings.Claude,
		}
	case string(agents.AgentCodex):
		return agents.ExecutionSettings{
			Codex: settings.Codex,
		}
	case string(agents.AgentGemini):
		return agents.ExecutionSettings{
			Gemini: settings.Gemini,
		}
	default:
		return agents.ExecutionSettings{}
	}
}

// ExecutionSettings resolves all configured adapter-specific execution settings.
func (c Config) ExecutionSettings() agents.ExecutionSettings {
	return agents.ExecutionSettings{
		Claude: agents.ClaudeExecutionSettings{
			Model:          c.Agents.Claude.Model,
			PermissionMode: c.Agents.Claude.PermissionMode,
		},
		Codex: agents.CodexExecutionSettings{
			Model:          c.Agents.Codex.Model,
			SandboxMode:    c.Agents.Codex.SandboxMode,
			ApprovalPolicy: c.Agents.Codex.ApprovalPolicy,
		},
		Gemini: agents.GeminiExecutionSettings{
			ApprovalMode: c.Agents.Gemini.ApprovalMode,
			SandboxMode:  c.Agents.Gemini.SandboxMode,
			Model:        c.Agents.Gemini.Model,
		},
	}
}

// RecommendedExecutionSettings returns the default per-agent execution settings
// `springfield init` applies when the operator picks the recommended profile.
//
// Model is intentionally unset across all three agent blocks below. Pinning a
// model couples Springfield's release cadence to each vendor's; we defer to the
// underlying CLI's evolving default. Operators override per-agent via
// `springfield init`.
func RecommendedExecutionSettings() agents.ExecutionSettings {
	return agents.ExecutionSettings{
		Claude: agents.ClaudeExecutionSettings{
			PermissionMode: "bypassPermissions",
		},
		Codex: agents.CodexExecutionSettings{
			SandboxMode:    "danger-full-access",
			ApprovalPolicy: "never",
		},
		Gemini: agents.GeminiExecutionSettings{
			ApprovalMode: "yolo",
			SandboxMode:  "sandbox-exec",
		},
	}
}

func (c Config) ExecutionModes() AgentExecutionModes {
	return AgentExecutionModes{
		Claude: executionModeForClaude(c.Agents.Claude),
		Codex:  executionModeForCodex(c.Agents.Codex),
		Gemini: executionModeForGemini(c.Agents.Gemini),
	}
}

func (c Config) HasAnyExecutionSettings() bool {
	return c.Agents.Claude.isPresent ||
		c.Agents.Codex.isPresent ||
		c.Agents.Gemini.isPresent ||
		c.Agents.Claude.Model != "" ||
		c.Agents.Claude.PermissionMode != "" ||
		c.Agents.Codex.Model != "" ||
		c.Agents.Codex.SandboxMode != "" ||
		c.Agents.Codex.ApprovalPolicy != "" ||
		c.Agents.Gemini.ApprovalMode != "" ||
		c.Agents.Gemini.SandboxMode != "" ||
		c.Agents.Gemini.Model != ""
}

func (c *Config) ApplyRecommendedExecutionDefaults() {
	recommended := RecommendedExecutionSettings()
	c.Agents.Claude.Model = recommended.Claude.Model
	c.Agents.Claude.PermissionMode = recommended.Claude.PermissionMode
	c.Agents.Codex.Model = recommended.Codex.Model
	c.Agents.Codex.SandboxMode = recommended.Codex.SandboxMode
	c.Agents.Codex.ApprovalPolicy = recommended.Codex.ApprovalPolicy
	c.Agents.Gemini.ApprovalMode = recommended.Gemini.ApprovalMode
	c.Agents.Gemini.SandboxMode = recommended.Gemini.SandboxMode
	c.Agents.Gemini.Model = recommended.Gemini.Model
}

func (c *Config) ApplyExecutionMode(agentID string, mode ExecutionMode) {
	switch agentID {
	case string(agents.AgentClaude):
		switch mode {
		case ExecutionModeRecommended:
			c.Agents.Claude.isPresent = true
			recommended := RecommendedExecutionSettings().Claude
			c.Agents.Claude.Model = recommended.Model
			c.Agents.Claude.PermissionMode = recommended.PermissionMode
		case ExecutionModeOff:
			c.Agents.Claude.isPresent = true
			c.Agents.Claude.Model = ""
			c.Agents.Claude.PermissionMode = ""
		}
	case string(agents.AgentCodex):
		switch mode {
		case ExecutionModeRecommended:
			c.Agents.Codex.isPresent = true
			recommended := RecommendedExecutionSettings().Codex
			c.Agents.Codex.Model = recommended.Model
			c.Agents.Codex.SandboxMode = recommended.SandboxMode
			c.Agents.Codex.ApprovalPolicy = recommended.ApprovalPolicy
		case ExecutionModeOff:
			c.Agents.Codex.isPresent = true
			c.Agents.Codex.Model = ""
			c.Agents.Codex.SandboxMode = ""
			c.Agents.Codex.ApprovalPolicy = ""
		}
	case string(agents.AgentGemini):
		switch mode {
		case ExecutionModeRecommended:
			c.Agents.Gemini.isPresent = true
			recommended := RecommendedExecutionSettings().Gemini
			c.Agents.Gemini.ApprovalMode = recommended.ApprovalMode
			c.Agents.Gemini.SandboxMode = recommended.SandboxMode
			c.Agents.Gemini.Model = recommended.Model
		case ExecutionModeOff:
			c.Agents.Gemini.isPresent = true
			c.Agents.Gemini.ApprovalMode = ""
			c.Agents.Gemini.SandboxMode = ""
			c.Agents.Gemini.Model = ""
		}
	}
}

func executionModeForClaude(cfg ClaudeAgentConfig) ExecutionMode {
	switch {
	case cfg.PermissionMode == "bypassPermissions" && cfg.Model == "":
		return ExecutionModeRecommended
	case cfg.PermissionMode == "" && cfg.Model == "":
		return ExecutionModeOff
	default:
		return ExecutionModeCustom
	}
}

func executionModeForCodex(cfg CodexAgentConfig) ExecutionMode {
	if cfg.SandboxMode == "danger-full-access" && cfg.ApprovalPolicy == "never" && cfg.Model == "" {
		return ExecutionModeRecommended
	}
	if cfg.SandboxMode == "" && cfg.ApprovalPolicy == "" && cfg.Model == "" {
		return ExecutionModeOff
	}
	return ExecutionModeCustom
}

func executionModeForGemini(cfg GeminiAgentConfig) ExecutionMode {
	if cfg.ApprovalMode == "yolo" && cfg.SandboxMode == "sandbox-exec" && cfg.Model == "" {
		return ExecutionModeRecommended
	}
	if cfg.ApprovalMode == "" && cfg.SandboxMode == "" && cfg.Model == "" {
		return ExecutionModeOff
	}
	return ExecutionModeCustom
}

// PlanConfig stores per-plan overrides.
type PlanConfig struct {
	Agent string `toml:"agent"`
}

// AgentsConfig stores adapter-specific execution settings.
type AgentsConfig struct {
	Claude ClaudeAgentConfig `toml:"claude"`
	Codex  CodexAgentConfig  `toml:"codex"`
	Gemini GeminiAgentConfig `toml:"gemini"`
}

// ClaudeAgentConfig stores supported Claude execution settings.
type ClaudeAgentConfig struct {
	Model          string `toml:"model,omitempty"`
	PermissionMode string `toml:"permission_mode,omitempty"`
	isPresent      bool   `toml:"-"`
}

// CodexAgentConfig stores supported Codex execution settings.
type CodexAgentConfig struct {
	Model          string `toml:"model,omitempty"`
	SandboxMode    string `toml:"sandbox_mode,omitempty"`
	ApprovalPolicy string `toml:"approval_policy,omitempty"`
	isPresent      bool   `toml:"-"`
}

// GeminiAgentConfig stores supported Gemini execution settings.
type GeminiAgentConfig struct {
	ApprovalMode string `toml:"approval_mode,omitempty"`
	SandboxMode  string `toml:"sandbox_mode,omitempty"`
	Model        string `toml:"model,omitempty"`
	isPresent    bool   `toml:"-"`
}

// Loaded is the stable public result of a config load.
type Loaded struct {
	RootDir string
	Path    string
	Config  Config
}
