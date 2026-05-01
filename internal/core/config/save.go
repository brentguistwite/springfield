package config

import (
	"bytes"

	"github.com/BurntSushi/toml"
)

type saveConfig struct {
	Project ProjectConfig         `toml:"project"`
	Agents  *saveAgentsConfig     `toml:"agents,omitempty"`
	Plans   map[string]PlanConfig `toml:"plans"`
	Start   *saveStartConfig      `toml:"start,omitempty"`
}

type saveAgentsConfig struct {
	Claude *ClaudeAgentConfig `toml:"claude,omitempty"`
	Codex  *CodexAgentConfig  `toml:"codex,omitempty"`
	Gemini *GeminiAgentConfig `toml:"gemini,omitempty"`
}

type saveStartConfig struct {
	KeepAwake *bool `toml:"keep_awake,omitempty"`
}

// Save writes the config back to disk.
func Save(loaded Loaded) error {
	normalize(&loaded.Config)

	var buf bytes.Buffer
	encoder := toml.NewEncoder(&buf)
	if err := encoder.Encode(newSaveConfig(loaded.Config)); err != nil {
		return err
	}

	return writeFileAtomic(loaded.Path, buf.Bytes(), 0644)
}

func newSaveConfig(cfg Config) saveConfig {
	out := saveConfig{
		Project: cfg.Project,
		Plans:   cfg.Plans,
	}

	var agentsCfg *saveAgentsConfig
	if cfg.Agents.Claude.isPresent || cfg.Agents.Claude.Model != "" || cfg.Agents.Claude.PermissionMode != "" {
		agentsCfg = &saveAgentsConfig{
			Claude: &ClaudeAgentConfig{
				Model:          cfg.Agents.Claude.Model,
				PermissionMode: cfg.Agents.Claude.PermissionMode,
			},
		}
	}
	if cfg.Agents.Codex.isPresent || cfg.Agents.Codex.Model != "" || cfg.Agents.Codex.SandboxMode != "" || cfg.Agents.Codex.ApprovalPolicy != "" {
		if agentsCfg == nil {
			agentsCfg = &saveAgentsConfig{}
		}
		agentsCfg.Codex = &CodexAgentConfig{
			Model:          cfg.Agents.Codex.Model,
			SandboxMode:    cfg.Agents.Codex.SandboxMode,
			ApprovalPolicy: cfg.Agents.Codex.ApprovalPolicy,
		}
	}
	if cfg.Agents.Gemini.isPresent || cfg.Agents.Gemini.ApprovalMode != "" || cfg.Agents.Gemini.SandboxMode != "" || cfg.Agents.Gemini.Model != "" {
		if agentsCfg == nil {
			agentsCfg = &saveAgentsConfig{}
		}
		agentsCfg.Gemini = &GeminiAgentConfig{
			ApprovalMode: cfg.Agents.Gemini.ApprovalMode,
			SandboxMode:  cfg.Agents.Gemini.SandboxMode,
			Model:        cfg.Agents.Gemini.Model,
		}
	}
	out.Agents = agentsCfg

	if cfg.Start.KeepAwake != nil {
		out.Start = &saveStartConfig{KeepAwake: cfg.Start.KeepAwake}
	}

	return out
}
