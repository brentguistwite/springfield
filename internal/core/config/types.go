package config

import "springfield/internal/core/agents"

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
			PermissionMode: c.Agents.Claude.PermissionMode,
		},
		Codex: agents.CodexExecutionSettings{
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
			Model:        "",
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
		c.Agents.Claude.PermissionMode != "" ||
		c.Agents.Codex.SandboxMode != "" ||
		c.Agents.Codex.ApprovalPolicy != "" ||
		c.Agents.Gemini.ApprovalMode != "" ||
		c.Agents.Gemini.SandboxMode != "" ||
		c.Agents.Gemini.Model != ""
}

func (c *Config) ApplyRecommendedExecutionDefaults() {
	recommended := RecommendedExecutionSettings()
	c.Agents.Claude.PermissionMode = recommended.Claude.PermissionMode
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
			c.Agents.Claude.PermissionMode = RecommendedExecutionSettings().Claude.PermissionMode
		case ExecutionModeOff:
			c.Agents.Claude.isPresent = true
			c.Agents.Claude.PermissionMode = ""
		}
	case string(agents.AgentCodex):
		switch mode {
		case ExecutionModeRecommended:
			c.Agents.Codex.isPresent = true
			recommended := RecommendedExecutionSettings().Codex
			c.Agents.Codex.SandboxMode = recommended.SandboxMode
			c.Agents.Codex.ApprovalPolicy = recommended.ApprovalPolicy
		case ExecutionModeOff:
			c.Agents.Codex.isPresent = true
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
	switch cfg.PermissionMode {
	case "bypassPermissions":
		return ExecutionModeRecommended
	case "":
		return ExecutionModeOff
	default:
		return ExecutionModeCustom
	}
}

func executionModeForCodex(cfg CodexAgentConfig) ExecutionMode {
	if cfg.SandboxMode == "danger-full-access" && cfg.ApprovalPolicy == "never" {
		return ExecutionModeRecommended
	}
	if cfg.SandboxMode == "" && cfg.ApprovalPolicy == "" {
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
	PermissionMode string `toml:"permission_mode,omitempty"`
	isPresent      bool   `toml:"-"`
}

// CodexAgentConfig stores supported Codex execution settings.
type CodexAgentConfig struct {
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
