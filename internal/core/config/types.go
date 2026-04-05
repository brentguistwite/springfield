package config

// Config is the shared project configuration loaded from springfield.toml.
type Config struct {
	Project ProjectConfig         `toml:"project"`
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
	DefaultAgent string `toml:"default_agent"`
}

// PlanConfig stores per-plan overrides.
type PlanConfig struct {
	Agent string `toml:"agent"`
}

// Loaded is the stable public result of a config load.
type Loaded struct {
	RootDir string
	Path    string
	Config  Config
}
