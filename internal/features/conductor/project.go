package conductor

import (
	"errors"
	"os"
	"time"

	"springfield/internal/storage"
)

const (
	configPath       = "execution/config.json"
	statePath        = "execution/state.json"
	legacyConfigPath = "conductor/config.json"
	legacyStatePath  = "conductor/state.json"
)

// Project owns conductor config and state for one Springfield project.
type Project struct {
	runtime storage.Runtime
	Config  *Config
	State   *State
}

// LoadProject resolves the Springfield project root from startDir, then loads
// conductor config and state from project-local runtime storage.
func LoadProject(startDir string) (*Project, error) {
	runtime, err := storage.ResolveFrom(startDir)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if _, err := readConfig(runtime, &cfg); err != nil {
		return nil, err
	}

	state := NewState()
	if _, err := readState(runtime, state); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	return &Project{
		runtime: runtime,
		Config:  &cfg,
		State:   state,
	}, nil
}

// SaveConfig persists conductor config.
func (p *Project) SaveConfig() error {
	return p.runtime.WriteJSON(configPath, p.Config)
}

// SaveState persists conductor state.
func (p *Project) SaveState() error {
	return p.runtime.WriteJSON(statePath, p.State)
}

// AllPlans returns sequential plans followed by flattened batch plans.
func (p *Project) AllPlans() []string {
	plans := make([]string, 0, len(p.Config.Sequential))
	plans = append(plans, p.Config.Sequential...)
	for _, batch := range p.Config.Batches {
		plans = append(plans, batch...)
	}
	return plans
}

// PlanStatus returns the current status for name, defaulting to pending.
func (p *Project) PlanStatus(name string) PlanStatus {
	if plan, ok := p.State.Plans[name]; ok {
		return plan.Status
	}

	return StatusPending
}

// PlanError returns the current error for name, if any.
func (p *Project) PlanError(name string) string {
	if plan, ok := p.State.Plans[name]; ok {
		return plan.Error
	}

	return ""
}

// PlanAgent returns the agent used for name, if any.
func (p *Project) PlanAgent(name string) string {
	if plan, ok := p.State.Plans[name]; ok {
		return plan.Agent
	}
	return ""
}

// PlanEvidencePath returns the evidence path for name, if any.
func (p *Project) PlanEvidencePath(name string) string {
	if plan, ok := p.State.Plans[name]; ok {
		return plan.EvidencePath
	}
	return ""
}

// PlanAttempts returns the attempt count for name.
func (p *Project) PlanAttempts(name string) int {
	if plan, ok := p.State.Plans[name]; ok {
		return plan.Attempts
	}
	return 0
}

func (p *Project) ensurePlan(name string) *PlanState {
	if plan, ok := p.State.Plans[name]; ok {
		return plan
	}

	plan := &PlanState{Status: StatusPending}
	p.State.Plans[name] = plan
	return plan
}

// MarkRunning records running status for name.
func (p *Project) MarkRunning(name string) {
	plan := p.ensurePlan(name)
	plan.Status = StatusRunning
	plan.Error = ""
	plan.StartedAt = time.Now()
	plan.EndedAt = time.Time{}
	plan.Attempts++
}

// MarkCompleted records completed status, agent, and end time for name.
func (p *Project) MarkCompleted(name, agent string) {
	plan := p.ensurePlan(name)
	plan.Status = StatusCompleted
	plan.Error = ""
	plan.Agent = agent
	plan.EndedAt = time.Now()
}

// MarkFailed records failed status, reason, agent, evidence path, and end time for name.
func (p *Project) MarkFailed(name, reason, agent, evidencePath string) {
	plan := p.ensurePlan(name)
	plan.Status = StatusFailed
	plan.Error = reason
	plan.Agent = agent
	plan.EvidencePath = evidencePath
	plan.EndedAt = time.Now()
}

// ResetState clears execution progress and starts fresh.
func (p *Project) ResetState() {
	p.State = NewState()
}

func readConfig(runtime storage.Runtime, cfg *Config) (string, error) {
	return readJSONCompat(runtime, cfg, configPath, legacyConfigPath)
}

func readState(runtime storage.Runtime, state *State) (string, error) {
	return readJSONCompat(runtime, state, statePath, legacyStatePath)
}

func readJSONCompat(runtime storage.Runtime, target any, paths ...string) (string, error) {
	var lastErr error
	for _, path := range paths {
		err := runtime.ReadJSON(path, target)
		if err == nil {
			return path, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			lastErr = err
			continue
		}
		return "", err
	}
	return "", lastErr
}
