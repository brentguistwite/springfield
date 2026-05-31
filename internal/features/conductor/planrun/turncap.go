package planrun

import (
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
)

// TurnCapExceededReason is the structured exit-reason tag recorded when an
// iteration burns more agent turns than the configured cap without completing.
// Operators see it in `springfield status` and the per-plan summary.json.
//
// The constant lives in the runtime package now (the runtime layer is what
// actually synthesizes the failure so the agent_priority fallback chain can
// fire on a turn-cap trip). Re-exported here so existing callers and tests
// that referenced planrun.TurnCapExceededReason keep working.
const TurnCapExceededReason = coreruntime.TurnCapExceededReason

// EnforceTurnCap delegates to [coreruntime.EnforceTurnCap]. Kept as a
// package-level wrapper so existing planrun-internal callers and tests do not
// need to reimport; the actual logic and ownership lives with the runtime.
//
// The runtime layer now applies the same check internally on every Runner.Run
// (when [coreruntime.Request.MaxTurnsPerIteration] > 0), so a planrun caller
// generally does NOT need to re-invoke this helper after the run — the
// synthesized failure already flowed through the agent classifier and the
// fallback chain. The wrapper remains exported only for test ergonomics.
func EnforceTurnCap(events []coreexec.Event, maxTurns int, completeHonored bool) error {
	return coreruntime.EnforceTurnCap(events, maxTurns, completeHonored)
}
