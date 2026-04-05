package conductor

type phase struct {
	plans []string
}

// Schedule models the derived execution order for a conductor config.
type Schedule struct {
	phases []phase
}

// BuildSchedule derives sequential single-plan phases plus batch phases.
func BuildSchedule(cfg *Config) *Schedule {
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

// NextPlans returns the current phase's incomplete plans, or nil when done.
func (s *Schedule) NextPlans(state *State) []string {
	for _, phase := range s.phases {
		if phaseComplete(phase, state) {
			continue
		}

		next := make([]string, 0, len(phase.plans))
		for _, name := range phase.plans {
			if planStatus(name, state) != StatusCompleted {
				next = append(next, name)
			}
		}
		return next
	}

	return nil
}

// IsComplete reports whether every configured plan completed.
func (s *Schedule) IsComplete(state *State) bool {
	for _, phase := range s.phases {
		if !phaseComplete(phase, state) {
			return false
		}
	}

	return true
}

// Progress returns completed and total plan counts.
func (s *Schedule) Progress(state *State) (completed, total int) {
	for _, phase := range s.phases {
		for _, name := range phase.plans {
			total++
			if planStatus(name, state) == StatusCompleted {
				completed++
			}
		}
	}

	return completed, total
}

func phaseComplete(phase phase, state *State) bool {
	for _, name := range phase.plans {
		if planStatus(name, state) != StatusCompleted {
			return false
		}
	}

	return true
}

func planStatus(name string, state *State) PlanStatus {
	if plan, ok := state.Plans[name]; ok {
		return plan.Status
	}

	return StatusPending
}
