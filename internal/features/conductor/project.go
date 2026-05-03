package conductor

import (
	"errors"
	"fmt"
	"os"
	"time"

	"springfield/internal/storage"
)

const (
	configPath = "execution/config.json"
	statePath  = "execution/state.json"
)

// Project owns conductor config and state for one Springfield project.
type Project struct {
	runtime storage.Runtime
	Config  *Config
	State   *State
}

// LoadProject resolves the Springfield project root from startDir, then loads
// conductor config and state from project-local runtime storage. Plan-unit
// invariants are validated; an invalid registry returns a structured error.
func LoadProject(startDir string) (*Project, error) {
	project, err := LoadProjectRaw(startDir)
	if err != nil {
		return nil, err
	}
	if err := ValidateConfigPlanUnits(project.Config, project.runtime.RootDir); err != nil {
		return nil, fmt.Errorf("invalid execution config: %w", err)
	}
	return project, nil
}

// LoadProjectRaw decodes config and state without validating plan_units.
// Use only from repair-oriented flows that need to fix an invalid registry
// without first hand-editing JSON. Production callers should use [LoadProject].
func LoadProjectRaw(startDir string) (*Project, error) {
	runtime, err := storage.ResolveFrom(startDir)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := runtime.ReadJSON(configPath, &cfg); err != nil {
		return nil, err
	}

	state := NewState()
	if err := runtime.ReadJSON(statePath, state); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	return &Project{
		runtime: runtime,
		Config:  &cfg,
		State:   state,
	}, nil
}

// SaveConfig persists conductor config after validating plan_units invariants.
func (p *Project) SaveConfig() error {
	if err := ValidateConfigPlanUnits(p.Config, p.runtime.RootDir); err != nil {
		return fmt.Errorf("invalid execution config: %w", err)
	}
	return p.runtime.WriteJSON(configPath, p.Config)
}

// SaveConfigUnchecked persists conductor config without validating plan_units.
// Repair flows use this so a partially fixed registry can be persisted even
// when other entries remain invalid; iterative repair eventually returns the
// registry to a state that passes [LoadProject].
func (p *Project) SaveConfigUnchecked() error {
	return p.runtime.WriteJSON(configPath, p.Config)
}

// SaveState persists conductor state.
func (p *Project) SaveState() error {
	return p.runtime.WriteJSON(statePath, p.State)
}

// AllPlans returns the configured plans in execution order.
//
// When PlanUnits is populated, those are returned in Order/ID order; otherwise
// the legacy Sequential then flattened-Batches projection is returned.
func (p *Project) AllPlans() []string {
	if len(p.Config.PlanUnits) > 0 {
		return OrderedPlanUnitIDs(p.Config.PlanUnits)
	}
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

// MarkCompleted records completed status, agent, evidence path, and end time for name.
func (p *Project) MarkCompleted(name, agent, evidencePath string) {
	plan := p.ensurePlan(name)
	plan.Status = StatusCompleted
	plan.Error = ""
	plan.Agent = agent
	plan.EvidencePath = evidencePath
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
