package conductor

type phase struct {
	plans []string
}

// Schedule models the derived execution order for a conductor config.
type Schedule struct {
	phases []phase
}

// BuildSchedule derives sequential single-plan phases plus batch phases.
//
// When cfg.PlanUnits is non-empty, the schedule is derived from PlanUnits in
// stable Order (ties broken by ID) and Sequential/Batches are ignored. This
// keeps one source of truth for execution order while preserving the legacy
// projection inputs in config.
func BuildSchedule(cfg *Config) *Schedule {
	if len(cfg.PlanUnits) > 0 {
		ordered := OrderedPlanUnitIDs(cfg.PlanUnits)
		phases := make([]phase, 0, len(ordered))
		for _, id := range ordered {
			phases = append(phases, phase{plans: []string{id}})
		}
		return &Schedule{phases: phases}
	}

	phases := make([]phase, 0, len(cfg.Sequential)+len(cfg.Batches))
	for _, name := range cfg.Sequential {
		phases = append(phases, phase{plans: []string{name}})
	}
	for _, batch := range cfg.Batches {
		if len(batch) == 0 {
			continue
		}
		phases = append(phases, phase{plans: batch})
	}

	return &Schedule{phases: phases}
}

// NextPlans returns the current phase's not-yet-integrated plans, or nil
// when every phase is integrated.
//
// "Integrated" is the truthful queue-advancement gate: execution Completed
// AND (Merge==nil OR Merge.Status==Succeeded) AND Cleanup did not fail. A
// plan whose execution finished but whose merge was refused/failed or whose
// cleanup failed is NOT eligible to advance and is returned again so the
// next surface (status, start) sees it as still in flight.
func (s *Schedule) NextPlans(state *State) []string {
	for _, phase := range s.phases {
		if phaseIntegrated(phase, state) {
			continue
		}

		next := make([]string, 0, len(phase.plans))
		for _, name := range phase.plans {
			if !planIntegrated(name, state) {
				next = append(next, name)
			}
		}
		return next
	}

	return nil
}

// IsComplete reports whether every configured plan is integrated (execution
// completed AND merge succeeded when applicable AND cleanup did not fail).
func (s *Schedule) IsComplete(state *State) bool {
	for _, phase := range s.phases {
		if !phaseIntegrated(phase, state) {
			return false
		}
	}

	return true
}

// Progress returns integrated and total plan counts.
func (s *Schedule) Progress(state *State) (completed, total int) {
	for _, phase := range s.phases {
		for _, name := range phase.plans {
			total++
			if planIntegrated(name, state) {
				completed++
			}
		}
	}

	return completed, total
}

func phaseIntegrated(phase phase, state *State) bool {
	for _, name := range phase.plans {
		if !planIntegrated(name, state) {
			return false
		}
	}

	return true
}

// planIntegrated reports whether a named plan is fully integrated. Missing
// entries default to "not integrated" so a never-run plan is still
// scheduled.
func planIntegrated(name string, state *State) bool {
	plan, ok := state.Plans[name]
	if !ok {
		return false
	}
	return plan.IsIntegrated()
}

func planStatus(name string, state *State) PlanStatus {
	if plan, ok := state.Plans[name]; ok {
		return plan.Status
	}

	return StatusPending
}
