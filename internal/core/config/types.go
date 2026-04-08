package config

import "springfield/internal/core/agents"

// Config is the shared project configuration loaded from springfield.toml.
type Config struct {
	Project ProjectConfig         `toml:"project"`
	Agents  AgentsConfig          `toml:"agents"`
	Plans   map[string]PlanConfig `toml:"plans"`
}

// AgentForPlan resolves the effective agent for a plan, falling back to the
// project default when no override exists.
func (c Config) AgentForPlan(planID string) string {
	if plan, ok := c.Plans[planID]; ok && plan.Agent != "" {
		return plan.Agent
	}

	return c.Project.DefaultAgent
}

// ProjectConfig stores project-wide defaults.
type ProjectConfig struct {
	DefaultAgent  string   `toml:"default_agent"`
	AgentPriority []string `toml:"agent_priority,omitempty"`
}

// EffectivePriority returns the agent priority list, falling back to
// [DefaultAgent] when no explicit priority is configured.
func (c Config) EffectivePriority() []string {
	if len(c.Project.AgentPriority) > 0 {
		return c.Project.AgentPriority
	}
	return []string{c.Project.DefaultAgent}
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
	}
}

// PlanConfig stores per-plan overrides.
type PlanConfig struct {
	Agent string `toml:"agent"`
}

// AgentsConfig stores adapter-specific execution settings.
type AgentsConfig struct {
	Claude ClaudeAgentConfig `toml:"claude"`
	Codex  CodexAgentConfig  `toml:"codex"`
}

// ClaudeAgentConfig stores supported Claude execution settings.
type ClaudeAgentConfig struct {
	PermissionMode string `toml:"permission_mode,omitempty"`
}

// CodexAgentConfig stores supported Codex execution settings.
type CodexAgentConfig struct {
	SandboxMode    string `toml:"sandbox_mode,omitempty"`
	ApprovalPolicy string `toml:"approval_policy,omitempty"`
}

// Loaded is the stable public result of a config load.
type Loaded struct {
	RootDir string
	Path    string
	Config  Config
}
