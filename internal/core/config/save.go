package config

import (
	"bytes"
	"os"

	"github.com/BurntSushi/toml"
)

type saveConfig struct {
	Project ProjectConfig         `toml:"project"`
	Agents  *saveAgentsConfig     `toml:"agents,omitempty"`
	Plans   map[string]PlanConfig `toml:"plans"`
}

type saveAgentsConfig struct {
	Claude *ClaudeAgentConfig `toml:"claude,omitempty"`
	Codex  *CodexAgentConfig  `toml:"codex,omitempty"`
}

// Save writes the config back to disk. When AgentPriority is set, it syncs
// DefaultAgent to priority[0] for backwards compatibility.
// The sync modifies a local copy; callers should reload via LoadFrom if they
// need the in-memory Config to reflect the change.
func Save(loaded Loaded) error {
	if len(loaded.Config.Project.AgentPriority) > 0 {
		loaded.Config.Project.DefaultAgent = loaded.Config.Project.AgentPriority[0]
	}
	normalize(&loaded.Config)

	var buf bytes.Buffer
	encoder := toml.NewEncoder(&buf)
	if err := encoder.Encode(newSaveConfig(loaded.Config)); err != nil {
		return err
	}

	return os.WriteFile(loaded.Path, buf.Bytes(), 0644)
}

func newSaveConfig(cfg Config) saveConfig {
	out := saveConfig{
		Project: cfg.Project,
		Plans:   cfg.Plans,
	}

	var agentsCfg *saveAgentsConfig
	if cfg.Agents.Claude.PermissionMode != "" {
		agentsCfg = &saveAgentsConfig{
			Claude: &ClaudeAgentConfig{PermissionMode: cfg.Agents.Claude.PermissionMode},
		}
	}
	if cfg.Agents.Codex.SandboxMode != "" || cfg.Agents.Codex.ApprovalPolicy != "" {
		if agentsCfg == nil {
			agentsCfg = &saveAgentsConfig{}
		}
		agentsCfg.Codex = &CodexAgentConfig{
			SandboxMode:    cfg.Agents.Codex.SandboxMode,
			ApprovalPolicy: cfg.Agents.Codex.ApprovalPolicy,
		}
	}
	out.Agents = agentsCfg

	return out
}
