package retro

import (
	"fmt"
	"path"
	"strings"
)

// Classify runs the pattern-key rules over an extracted [Report] and returns
// the findings in a stable, table-declaration order.
//
// It is deliberately pure over its inputs — a Report and a slice of prior batch
// totals — so it can be tested at the boundary without touching disk. Extract
// leaves Report.Findings empty on purpose (a degraded extraction still yields a
// valid report); classification is the separate, side-effect-free step a caller
// runs over that report, keeping the two concerns independently testable.
//
// One Finding is emitted per pattern key that any plan (or the batch) trips,
// aggregating every matching plan's ID and evidence refs into that single
// finding — findings are batch-scoped because the conductor reuses plan IDs
// across batches, so the Report (one batch) is the only sound container to key
// them by. priorTotalsUSD carries the TotalUSD of earlier archived batches and
// is consulted only by the cost-overrun rule, which needs a spend baseline and
// is skipped when fewer than two priors exist; pass nil when none are known.
func Classify(r *Report, priorTotalsUSD []float64) []Finding {
	if r == nil {
		return nil
	}

	var findings []Finding

	// Per-plan rules, in table order. Each rule is asked about every plan; the
	// matches for one key fold into a single finding so a consumer sees one
	// "iteration-cap" entry naming three plans rather than three entries.
	for _, rule := range planRules {
		var planIDs, refs []string
		for _, p := range r.Plans {
			ok, evidence := rule.match(r, p)
			if !ok {
				continue
			}
			planIDs = append(planIDs, p.ID)
			for _, e := range evidence {
				// Qualify each ref with the plan it came from so an aggregated
				// finding's refs stay unambiguous across plans.
				refs = append(refs, path.Join(p.ID, e))
			}
		}
		if len(planIDs) == 0 {
			continue
		}
		findings = append(findings, Finding{
			PatternKey:   rule.key,
			Severity:     rule.severity,
			PlanIDs:      planIDs,
			EvidenceRefs: refs,
			Summary:      rule.summary,
		})
	}

	// Cost-overrun is the one batch-level rule: it compares this batch's total
	// against a baseline drawn from other batches, so it has no per-plan match
	// and lives outside the plan table. Appended last to keep ordering stable.
	if f, ok := classifyCostOverrun(r, priorTotalsUSD); ok {
		findings = append(findings, f)
	}

	return findings
}

// Severity is a small, stable scale so consumers can triage without parsing the
// pattern key. "critical" is a trust/safety breach (tamper), "error" is a run
// that could not do its job (setup died, merge refused), "warning" is a run that
// finished but burned budget or needed a human.
const (
	severityCritical = "critical"
	severityError    = "error"
	severityWarning  = "warning"
)

// The nine stable pattern keys. They are the retro package's public contract
// with any downstream index, so they are declared once here and never inlined.
const (
	patternIterationCap         = "iteration-cap"
	patternStallWedge           = "stall-wedge"
	patternVerifyNonconvergence = "verify-nonconvergence"
	patternReviewNeedsHuman     = "review-needs-human"
	patternFallbackStorm        = "fallback-storm"
	patternCostOverrun          = "cost-overrun"
	patternMergeRefused         = "merge-refused"
	patternSetupFailure         = "setup-failure"
	patternTamperDetected       = "tamper-detected"
)

// iterationCapExitReason is the exact ExitReason the runner writes to summary.json
// when a plan exhausts its iteration budget without a completion marker. Pinned
// as a constant (mirroring the runner's literal) so a wording drift there fails a
// test here rather than silently dropping the iteration-cap classification.
const iterationCapExitReason = "iteration cap reached without completion marker"

// fallbackStormThreshold is the number of fallback events (an iteration handing
// off from its primary agent to a backup) a plan must show before it counts as a
// storm. One fallback is routine adapter resilience; two or more means the
// primary repeatedly could not carry the work, which is the signal worth a
// finding — so the threshold is 2, and a lone claude→codex handoff stays quiet.
const fallbackStormThreshold = 2

// planRule is one row of the classifier table: a stable pattern key, its
// severity, a human summary, and a predicate over a single plan within its
// report. match returns whether the rule fires and the evidence tokens (file
// names, unqualified) that tripped it; Classify qualifies those with the plan ID.
// The whole report is passed too because some signals (merge-refused) are only
// legible against batch-level context like the branch-output mode.
type planRule struct {
	key      string
	severity string
	summary  string
	match    func(r *Report, p PlanRetro) (bool, []string)
}

// planRules is the single source of truth for the per-plan pattern keys. Adding
// a key is adding a row; the ordering here is the ordering findings come out in.
var planRules = []planRule{
	{
		key:      patternIterationCap,
		severity: severityWarning,
		summary:  "plan exhausted its iteration budget without emitting a completion marker",
		match: func(_ *Report, p PlanRetro) (bool, []string) {
			if p.ExitReason == iterationCapExitReason {
				return true, []string{"summary.json"}
			}
			return false, nil
		},
	},
	{
		key:      patternStallWedge,
		severity: severityWarning,
		summary:  "plan wedged: stall detection recorded no forward progress across iterations",
		match: func(_ *Report, p PlanRetro) (bool, []string) {
			// A stalls.jsonl wedge record is the raw signal; its presence (count
			// > 0) is enough — the payload is not needed to name the pattern.
			if p.Stalls > 0 {
				return true, []string{"stalls.jsonl"}
			}
			return false, nil
		},
	},
	{
		key:      patternVerifyNonconvergence,
		severity: severityWarning,
		summary:  "verify gate never converged (halted for a human, or repeated non-zero rounds)",
		match: func(_ *Report, p PlanRetro) (bool, []string) {
			// Two paths converge on the same pattern: an explicit human/error halt
			// recorded as the exit reason, or a run of failing verify rounds that
			// never reached green even without a formal halt.
			if p.ExitReason == "verify-needs-human" || p.ExitReason == "verify-errored" {
				return true, []string{"summary.json"}
			}
			if refs := failedVerifyRefs(p); len(refs) >= 2 {
				return true, refs
			}
			return false, nil
		},
	},
	{
		key:      patternReviewNeedsHuman,
		severity: severityWarning,
		summary:  "pre-merge review could not converge and halted for a human",
		match: func(_ *Report, p PlanRetro) (bool, []string) {
			if p.ExitReason == "review-needs-human" || p.ExitReason == "review-errored" {
				return true, []string{"summary.json"}
			}
			return false, nil
		},
	},
	{
		key:      patternFallbackStorm,
		severity: severityWarning,
		summary:  "primary agent repeatedly fell through to backup agents",
		match: func(_ *Report, p PlanRetro) (bool, []string) {
			events, refs := fallbackEvents(p)
			if events >= fallbackStormThreshold {
				return true, refs
			}
			return false, nil
		},
	},
	{
		key:      patternMergeRefused,
		severity: severityError,
		summary:  "completed plan's merge was refused; its branch was retained instead of merged-and-deleted",
		match: func(r *Report, p PlanRetro) (bool, []string) {
			// Consolidate mode deletes a plan's branch on a successful merge, so a
			// COMPLETED consolidate plan that still carries its branch is one whose
			// merge was refused/failed (cleanup preserves the branch on refusal).
			// Per-plan mode keeps branches by design, so the same retained-branch
			// shape there is normal — reading this signal only in consolidate mode
			// avoids a false merge-refused on every per-plan branch.
			if r.BatchMode != "consolidate" {
				return false, nil
			}
			if p.Branch == "" || !planCompleted(p) {
				return false, nil
			}
			return true, []string{"archive.json"}
		},
	},
	{
		key:      patternSetupFailure,
		severity: severityError,
		summary:  "plan failed in preflight setup before its agent loop ran",
		match: func(_ *Report, p PlanRetro) (bool, []string) {
			if isSetupExitReason(p.ExitReason) {
				return true, []string{"summary.json"}
			}
			// The archived record drops the fine-grained preflight tag, so the
			// surviving fingerprint of a setup death is a failed plan that never
			// produced any evidence (no summary.json, no iterations): the loop
			// never ran. An in-loop failure leaves an exit reason and evidence.
			if p.Status == "failed" && p.EvidenceMissing && p.ExitReason == "" && p.TerminalStatus == "" {
				return true, nil
			}
			return false, nil
		},
	},
	{
		key:      patternTamperDetected,
		severity: severityCritical,
		summary:  "tamper guard tripped: changes detected outside the agent's allowed surface",
		match: func(_ *Report, p PlanRetro) (bool, []string) {
			// The runner writes "tamper-detected: <reason>" for a detected tamper
			// and "tamper-guard-{snapshot,detect}-failed" when the guard itself
			// could not run — both are trust-boundary events under this key.
			if strings.HasPrefix(p.ExitReason, "tamper-detected") ||
				p.ExitReason == "tamper-guard-snapshot-failed" ||
				p.ExitReason == "tamper-guard-detect-failed" {
				return true, []string{"summary.json"}
			}
			return false, nil
		},
	},
}

// setupExitReasons are the preflight tags the runner records when a plan dies
// before its agent loop starts. They map to setup-failure rather than a
// per-gate key because none of them reflect agent work.
var setupExitReasons = map[string]bool{
	"setup-failed":           true,
	"prd-load-failed":        true,
	"prd-zero-stories":       true,
	"worktree-create-failed": true,
	"prompt-build-failed":    true,
	"save-state-failed":      true,
}

func isSetupExitReason(er string) bool { return setupExitReasons[er] }

// planCompleted reports whether the plan reached a completed terminal state,
// tolerating either the archive record status or the summary.json terminal
// status carrying the signal (they are written by different layers).
func planCompleted(p PlanRetro) bool {
	return p.Status == "completed" || p.TerminalStatus == "completed"
}

// failedVerifyRefs returns "verify-iter-<round>" tokens for every verify round
// that ended non-zero or timed out. A run of these is the non-convergence signal
// even when no formal human halt was recorded.
func failedVerifyRefs(p PlanRetro) []string {
	var refs []string
	for _, v := range p.VerifyRounds {
		if v.ExitCode != 0 || v.TimedOut {
			refs = append(refs, fmt.Sprintf("verify-iter-%d", v.Round))
		}
	}
	return refs
}

// fallbackEvents counts how many times a plan handed off from its primary agent
// to a backup (each extra attempt in an iteration's chain is one event) and
// returns the "iter-<n>" tokens for the iterations that did so. A single-attempt
// iteration contributes nothing.
func fallbackEvents(p PlanRetro) (events int, refs []string) {
	for _, it := range p.Iterations {
		if extra := len(it.Attempts) - 1; extra > 0 {
			events += extra
			refs = append(refs, fmt.Sprintf("iter-%d", it.Index))
		}
	}
	return events, refs
}

// classifyCostOverrun fires when this batch's spend runs more than 2x the mean
// of the prior batches. It is skipped entirely with fewer than two priors — a
// single prior is too thin a baseline to call anything an overrun — matching the
// "when >= 2 priors exist, else skip" contract.
func classifyCostOverrun(r *Report, priorTotalsUSD []float64) (Finding, bool) {
	if len(priorTotalsUSD) < 2 {
		return Finding{}, false
	}
	var sum float64
	for _, v := range priorTotalsUSD {
		sum += v
	}
	mean := sum / float64(len(priorTotalsUSD))
	if mean <= 0 || r.TotalUSD <= 2*mean {
		return Finding{}, false
	}
	return Finding{
		PatternKey:   patternCostOverrun,
		Severity:     severityWarning,
		EvidenceRefs: []string{"archive.json"},
		Summary: fmt.Sprintf("batch spend $%.2f exceeds 2x the $%.2f mean of %d prior batches",
			r.TotalUSD, mean, len(priorTotalsUSD)),
	}, true
}
