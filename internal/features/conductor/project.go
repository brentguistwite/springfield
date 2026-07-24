package conductor

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"springfield/internal/storage"
)

const (
	configPath = "execution/config.json"
	statePath  = "execution/state.json"
)

// Project owns conductor config and state for one Springfield project.
//
// Concurrency: parallel plan execution mutates State from multiple
// goroutines. All access to State.Plans on a concurrently-executed path MUST
// go through the locked API (UpdatePlan / ReadPlan / PlansSnapshot / the
// Plan* accessors / SaveState) — a bare map read races with any entry
// insertion, and a bare field write races with SaveState's marshal.
// Sequential-only paths (status, recover, plan compilation — separate
// processes, one goroutine) may keep touching State directly.
type Project struct {
	runtime storage.Runtime
	// mu guards State.Plans (map + entry fields) and marshal-during-save.
	mu     sync.Mutex
	Config *Config
	State  *State
}

// UpdatePlan runs fn on the named plan's state under the project lock,
// creating a pending entry if none exists. This is the only way
// concurrently-executed code may mutate PlanState fields.
func (p *Project) UpdatePlan(name string, fn func(*PlanState)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(p.ensurePlanLocked(name))
}

// ReadPlan returns a detached copy of the named plan's state. The copy is
// safe to read and mutate without holding the lock; changes do not write
// back (use UpdatePlan for that).
func (p *Project) ReadPlan(name string) (PlanState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if plan, ok := p.State.Plans[name]; ok {
		return *plan, true
	}
	return PlanState{}, false
}

// PlansSnapshot returns a detached copy of the whole plan-state map (entries
// are copied, not aliased). Concurrently-executed code that needs a
// cross-plan view (e.g. worktree collision checks) reads the snapshot
// instead of the live map.
func (p *Project) PlansSnapshot() map[string]*PlanState {
	p.mu.Lock()
	defer p.mu.Unlock()
	snap := make(map[string]*PlanState, len(p.State.Plans))
	for name, plan := range p.State.Plans {
		cp := *plan
		snap[name] = &cp
	}
	return snap
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

// SaveState persists conductor state. The marshal runs under the project
// lock so it never observes a concurrent PlanState mutation mid-write.
func (p *Project) SaveState() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runtime.WriteJSON(statePath, p.State)
}

// AllPlans returns the configured plans in execution order.
func (p *Project) AllPlans() []string {
	return OrderedPlanUnitIDs(p.Config.PlanUnits)
}

// PlanStatus returns the current status for name, defaulting to pending.
func (p *Project) PlanStatus(name string) PlanStatus {
	if plan, ok := p.ReadPlan(name); ok {
		return plan.Status
	}

	return StatusPending
}

// PlanError returns the current error for name, if any.
func (p *Project) PlanError(name string) string {
	if plan, ok := p.ReadPlan(name); ok {
		return plan.Error
	}

	return ""
}

// PlanAgent returns the agent used for name, if any.
func (p *Project) PlanAgent(name string) string {
	if plan, ok := p.ReadPlan(name); ok {
		return plan.Agent
	}
	return ""
}

// PlanEvidencePath returns the evidence path for name, if any.
func (p *Project) PlanEvidencePath(name string) string {
	if plan, ok := p.ReadPlan(name); ok {
		return plan.EvidencePath
	}
	return ""
}

// PlanAttempts returns the attempt count for name.
func (p *Project) PlanAttempts(name string) int {
	if plan, ok := p.ReadPlan(name); ok {
		return plan.Attempts
	}
	return 0
}

// ensurePlanLocked returns the named entry, creating a pending one if
// missing. Callers must hold p.mu.
func (p *Project) ensurePlanLocked(name string) *PlanState {
	if plan, ok := p.State.Plans[name]; ok {
		return plan
	}

	plan := &PlanState{Status: StatusPending}
	p.State.Plans[name] = plan
	return plan
}

// MarkRunning records running status for name.
func (p *Project) MarkRunning(name string) {
	p.UpdatePlan(name, func(plan *PlanState) {
		plan.Status = StatusRunning
		plan.Error = ""
		plan.StartedAt = time.Now()
		plan.EndedAt = time.Time{}
		plan.Attempts++
	})
}

// MarkCompleted records completed status, agent, evidence path, and end time for name.
func (p *Project) MarkCompleted(name, agent, evidencePath string) {
	p.UpdatePlan(name, func(plan *PlanState) {
		plan.Status = StatusCompleted
		plan.Error = ""
		plan.Agent = agent
		plan.EvidencePath = evidencePath
		plan.EndedAt = time.Now()
	})
}

// MarkFailed records failed status, reason, agent, evidence path, and end time for name.
func (p *Project) MarkFailed(name, reason, agent, evidencePath string) {
	p.UpdatePlan(name, func(plan *PlanState) {
		plan.Status = StatusFailed
		plan.Error = reason
		plan.Agent = agent
		plan.EvidencePath = evidencePath
		plan.EndedAt = time.Now()
	})
}

// ResetState clears execution progress and starts fresh.
func (p *Project) ResetState() {
	p.State = NewState()
}
