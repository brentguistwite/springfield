package runtime

import (
	"encoding/json"
	"fmt"

	"springfield/internal/core/exec"
)

// TurnCapExceededReason is the structured tag recorded when a single agent
// invocation burns more turns than the configured cap without legitimately
// completing the iteration. Operators see it in `springfield status` and in
// per-plan summary.json; the runtime synthesizes an error carrying this
// prefix so the standard agent error classifier can recognize the failure
// as retryable and the fallback chain in [Runner.Run] kicks in.
const TurnCapExceededReason = "iteration-turn-cap-exceeded"

// resultTurnEvent is the subset of an agent's stream-json terminal "result"
// event that the turn-cap monitor reads. Claude Code reports num_turns there
// under --output-format stream-json. Agents that do not emit the field (codex,
// gemini, opencode) decode to 0 and are therefore never capped by this
// monitor — the cap only fires on evidence of real thrash.
type resultTurnEvent struct {
	Type     string `json:"type"`
	NumTurns int    `json:"num_turns"`
}

// scanNumTurns returns the highest num_turns reported across stdout result
// events, or 0 when no result event carries the field. Taking the max keeps
// the monitor honest if more than one result event is present in a transcript.
func scanNumTurns(events []exec.Event) int {
	max := 0
	for _, ev := range events {
		if ev.Type != exec.EventStdout {
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
// more than maxTurns agent turns without legitimately completing.
//
// completeHonored is the caller's authoritative "the work for this iteration
// is genuinely done" signal — true only when ALL stories pass AND the agent
// emitted <promise>COMPLETE</promise>. The marker alone cannot be trusted:
// ScanMarkers can't distinguish a HONORED COMPLETE from a PREMATURE COMPLETE
// (one emitted before all stories pass). Round 2 of adversarial review on
// PR #72 caught the bug: when an agent burns 200 turns without passing a
// story and emits COMPLETE anyway, the marker alone defused the cap, leaving
// the iteration cap (50×) as the only backstop. Now: COMPLETE only wins when
// the caller confirms it was honored.
//
// Semantics:
//
//   - maxTurns <= 0          → cap disabled; always returns nil.
//   - completeHonored        → returns nil regardless of turn count. A
//     legitimately completed iteration is never capped — the work the cap
//     exists to protect is already done.
//   - num_turns <= maxTurns  → returns nil.
//   - num_turns >  maxTurns  → returns a non-nil error tagged
//     [TurnCapExceededReason], carrying the observed count and the cap so the
//     operator-facing failure is self-explanatory.
//
// Exposed as a package-public helper so callers (planrun) that want to
// short-circuit before the runtime layer also can. The runtime layer itself
// invokes the same logic internally via [Runner.Run].
func EnforceTurnCap(events []exec.Event, maxTurns int, completeHonored bool) error {
	if maxTurns <= 0 {
		return nil
	}
	if completeHonored {
		return nil
	}
	if turns := scanNumTurns(events); turns > maxTurns {
		return fmt.Errorf("%s: iteration used %d turns without completing (cap %d)",
			TurnCapExceededReason, turns, maxTurns)
	}
	return nil
}
