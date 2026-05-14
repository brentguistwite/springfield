package batch

import "springfield/internal/features/conductor"

// Progress is the plan-centric rollup of batch execution derived from
// conductor.State. It is the canonical source of truth for what status
// renderers, start headers, and recovery diagnostics display.
//
// Done, InFlight, and Pending are plan IDs in the same first-seen order as
// Batch.PlanIDs. CurrentPhaseIdx is the first phase that still has a
// non-integrated plan, or -1 when every plan is integrated.
type Progress struct {
	TotalPlans       int
	DonePlans        int
	Done             []string
	InFlight         []string
	Pending          []string
	TotalPhases      int
	CurrentPhaseIdx  int
	ParallelInFlight bool
	AllDone          bool
}

// ComputeProgress produces a Progress rollup for the given batch against the
// supplied conductor state. The function is pure: no I/O, no logging.
//
// A nil state is treated as "no plans started yet" — every plan lands in
// Pending. A missing plan key in state.Plans is treated as pending.
func ComputeProgress(b Batch, state *conductor.State) Progress {
	p := Progress{
		TotalPlans:      len(b.PlanIDs),
		TotalPhases:     len(b.Phases),
		CurrentPhaseIdx: -1,
	}

	var plans map[string]*conductor.PlanState
	if state != nil {
		plans = state.Plans
	}

	for _, id := range b.PlanIDs {
		ps := plans[id]
		switch {
		case ps != nil && ps.IsIntegrated():
			p.Done = append(p.Done, id)
			p.DonePlans++
		case ps != nil && ps.Status == conductor.StatusRunning:
			p.InFlight = append(p.InFlight, id)
		default:
			p.Pending = append(p.Pending, id)
		}
	}

	for i, phase := range b.Phases {
		allIntegrated := true
		for _, id := range phase.Plans {
			ps := plans[id]
			if ps == nil || !ps.IsIntegrated() {
				allIntegrated = false
				break
			}
		}
		if !allIntegrated {
			p.CurrentPhaseIdx = i
			if phase.Mode == PhaseParallel && len(p.InFlight) > 1 {
				p.ParallelInFlight = true
			}
			break
		}
	}

	p.AllDone = p.TotalPlans > 0 && p.DonePlans == p.TotalPlans
	return p
}
