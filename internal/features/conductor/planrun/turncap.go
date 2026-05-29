package planrun

import (
	"encoding/json"
	"fmt"

	coreexec "springfield/internal/core/exec"
)

// TurnCapExceededReason is the structured exit-reason tag recorded when an
// iteration burns more agent turns than the configured cap without completing.
// Operators see it in `springfield status` and the per-plan summary.json.
const TurnCapExceededReason = "iteration-turn-cap-exceeded"

// resultTurnEvent is the subset of an agent's stream-json terminal "result"
// event that the turn-cap monitor reads. Claude Code reports num_turns there
// under --output-format stream-json. Agents that do not emit the field (codex,
// gemini) decode to 0 and are therefore never capped by this monitor — the cap
// only fires on evidence of real thrash.
type resultTurnEvent struct {
	Type     string `json:"type"`
	NumTurns int    `json:"num_turns"`
}

// scanNumTurns returns the highest num_turns reported across stdout result
// events, or 0 when no result event carries the field. Taking the max keeps
// the monitor honest if more than one result event is present in a transcript.
func scanNumTurns(events []coreexec.Event) int {
	max := 0
	for _, ev := range events {
		if ev.Type != coreexec.EventStdout {
			continue
		}
		var e resultTurnEvent
		if err := json.Unmarshal([]byte(ev.Data), &e); err != nil {
			continue
		}
		if e.Type == "result" && e.NumTurns > max {
			max = e.NumTurns
		}
	}
	return max
}

// EnforceTurnCap reports a synthesized failure when a single iteration burned
// more than maxTurns agent turns WITHOUT emitting <promise>COMPLETE</promise>.
// It is the num_turns-monitoring half of B2: the installed claude CLI exposes
// no --max-turns flag, so Springfield watches the stream-json result event and
// trips the breaker itself.
//
// Semantics:
//
//   - maxTurns <= 0          → cap disabled; always returns nil.
//   - COMPLETE present       → returns nil regardless of turn count. A
//     completed iteration is never capped — COMPLETE always wins, because the
//     work the cap exists to protect is already done.
//   - num_turns <= maxTurns  → returns nil.
//   - num_turns >  maxTurns  → returns a non-nil error tagged
//     [TurnCapExceededReason], carrying the observed count and the cap so the
//     operator-facing failure is self-explanatory.
func EnforceTurnCap(events []coreexec.Event, maxTurns int) error {
	if maxTurns <= 0 {
		return nil
	}
	if _, complete := ScanMarkers(events); complete {
		return nil
	}
	if turns := scanNumTurns(events); turns > maxTurns {
		return fmt.Errorf("%s: iteration used %d turns without completing (cap %d)",
			TurnCapExceededReason, turns, maxTurns)
	}
	return nil
}
