package retro_test

import (
	"path/filepath"
	"testing"

	"springfield/internal/features/retro"
)

// TestClassifyPatternKeys pins each of the nine stable pattern keys with a
// positive case (a report shaped so the key must fire) and a negative case (a
// nearby report shaped so it must not). Every case runs the whole classifier and
// asserts on the presence/absence of the one key under test, so a rule that
// over-fires onto a sibling shape is caught by that sibling's negative case.
func TestClassifyPatternKeys(t *testing.T) {
	cases := []struct {
		name     string
		key      string
		report   *retro.Report
		priors   []float64
		wantFire bool
	}{
		// iteration-cap: the runner's exact cap exit reason vs. a clean completion.
		{"iteration-cap/positive", "iteration-cap",
			report(plan("US-1", func(p *retro.PlanRetro) {
				p.ExitReason = "iteration cap reached without completion marker"
			})), nil, true},
		{"iteration-cap/negative", "iteration-cap",
			report(plan("US-1", func(p *retro.PlanRetro) { p.ExitReason = "completed" })), nil, false},

		// stall-wedge: any wedge record vs. none.
		{"stall-wedge/positive", "stall-wedge",
			report(plan("US-1", func(p *retro.PlanRetro) { p.Stalls = 3 })), nil, true},
		{"stall-wedge/negative", "stall-wedge",
			report(plan("US-1", func(p *retro.PlanRetro) { p.Stalls = 0 })), nil, false},

		// verify-nonconvergence: a human halt fires; a single failing round does not
		// (one bad round is not yet non-convergence — the rule needs a run of them).
		{"verify-nonconvergence/positive-halt", "verify-nonconvergence",
			report(plan("US-1", func(p *retro.PlanRetro) { p.ExitReason = "verify-needs-human" })), nil, true},
		{"verify-nonconvergence/positive-rounds", "verify-nonconvergence",
			report(plan("US-1", func(p *retro.PlanRetro) {
				p.VerifyRounds = []retro.VerifyRetro{{Round: 1, ExitCode: 1}, {Round: 2, ExitCode: 1}}
			})), nil, true},
		{"verify-nonconvergence/negative", "verify-nonconvergence",
			report(plan("US-1", func(p *retro.PlanRetro) {
				p.VerifyRounds = []retro.VerifyRetro{{Round: 1, ExitCode: 1}, {Round: 2, ExitCode: 0}}
			})), nil, false},

		// review-needs-human: review halt vs. verify halt (must not bleed across).
		{"review-needs-human/positive", "review-needs-human",
			report(plan("US-1", func(p *retro.PlanRetro) { p.ExitReason = "review-needs-human" })), nil, true},
		{"review-needs-human/negative", "review-needs-human",
			report(plan("US-1", func(p *retro.PlanRetro) { p.ExitReason = "verify-needs-human" })), nil, false},

		// fallback-storm: two fallback events fire; a lone claude→codex handoff does not.
		{"fallback-storm/positive", "fallback-storm",
			report(plan("US-1", func(p *retro.PlanRetro) {
				p.Iterations = []retro.IterationRetro{
					{Index: 1, Attempts: []string{"claude", "codex"}},
					{Index: 2, Attempts: []string{"claude", "codex"}},
				}
			})), nil, true},
		{"fallback-storm/negative", "fallback-storm",
			report(plan("US-1", func(p *retro.PlanRetro) {
				p.Iterations = []retro.IterationRetro{{Index: 1, Attempts: []string{"claude", "codex"}}}
			})), nil, false},

		// cost-overrun: >2x the prior mean with enough priors vs. one thin prior.
		{"cost-overrun/positive", "cost-overrun",
			reportBatch(10.0), []float64{1.0, 1.0}, true},
		{"cost-overrun/negative-within", "cost-overrun",
			reportBatch(1.5), []float64{1.0, 1.0}, false},
		{"cost-overrun/negative-too-few-priors", "cost-overrun",
			reportBatch(10.0), []float64{1.0}, false},

		// merge-refused: a completed consolidate plan that kept its branch fires;
		// the identical shape in per-plan mode (branches kept by design) does not.
		{"merge-refused/positive", "merge-refused",
			reportMode("consolidate", plan("US-1", func(p *retro.PlanRetro) {
				p.Status = "completed"
				p.Branch = "springfield/us-1"
			})), nil, true},
		{"merge-refused/negative-per-plan", "merge-refused",
			reportMode("per-plan", plan("US-1", func(p *retro.PlanRetro) {
				p.Status = "completed"
				p.Branch = "springfield/us-1"
			})), nil, false},

		// setup-failure: a preflight tag fires; an in-loop agent failure does not.
		{"setup-failure/positive", "setup-failure",
			report(plan("US-1", func(p *retro.PlanRetro) { p.ExitReason = "setup-failed" })), nil, true},
		{"setup-failure/negative", "setup-failure",
			report(plan("US-1", func(p *retro.PlanRetro) {
				p.Status = "failed"
				p.ExitReason = "agent-failed"
			})), nil, false},

		// tamper-detected: the guard-trip reason fires; a clean run does not.
		{"tamper-detected/positive", "tamper-detected",
			report(plan("US-1", func(p *retro.PlanRetro) {
				p.ExitReason = "tamper-detected: unexpected changes to .github/"
			})), nil, true},
		{"tamper-detected/negative", "tamper-detected",
			report(plan("US-1", func(p *retro.PlanRetro) { p.ExitReason = "completed" })), nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := retro.Classify(tc.report, tc.priors)
			got := hasKey(findings, tc.key)
			if got != tc.wantFire {
				t.Fatalf("Classify fired %q = %v, want %v (findings: %+v)", tc.key, got, tc.wantFire, findings)
			}
		})
	}
}

// TestClassifyFallbackStormFromFixture exercises fallback-storm end to end over
// a finalized archive fixture whose iterations fell through multiple agents
// (each attempt carrying its own cost.json), proving the classifier reads the
// real multi-attempt evidence Extract reconstructs, not just hand-built structs.
func TestClassifyFallbackStormFromFixture(t *testing.T) {
	report, err := retro.Extract(filepath.Join("testdata", "fallback-storm"))
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	findings := retro.Classify(report, nil)

	f, ok := findingByKey(findings, "fallback-storm")
	if !ok {
		t.Fatalf("fallback-storm not fired over multi-attempt fixture; findings: %+v", findings)
	}
	if len(f.PlanIDs) != 1 || f.PlanIDs[0] != "US-030" {
		t.Errorf("PlanIDs = %v, want [US-030]", f.PlanIDs)
	}
	// Both iterations fell back, so both must be cited as evidence.
	if !contains(f.EvidenceRefs, "US-030/iter-1") || !contains(f.EvidenceRefs, "US-030/iter-2") {
		t.Errorf("EvidenceRefs = %v, want both iter-1 and iter-2 cited", f.EvidenceRefs)
	}
}

// TestClassifyAggregatesPlansPerKey locks the one-finding-per-key contract: two
// plans tripping the same key fold into a single finding naming both, rather than
// two findings — findings are batch-scoped, not per-plan.
func TestClassifyAggregatesPlansPerKey(t *testing.T) {
	r := report(
		plan("US-1", func(p *retro.PlanRetro) { p.Stalls = 1 }),
		plan("US-2", func(p *retro.PlanRetro) { p.Stalls = 2 }),
	)
	findings := retro.Classify(r, nil)
	f, ok := findingByKey(findings, "stall-wedge")
	if !ok {
		t.Fatal("stall-wedge not fired")
	}
	if len(f.PlanIDs) != 2 || f.PlanIDs[0] != "US-1" || f.PlanIDs[1] != "US-2" {
		t.Errorf("PlanIDs = %v, want both plans in archive order [US-1 US-2]", f.PlanIDs)
	}
	if n := countKey(findings, "stall-wedge"); n != 1 {
		t.Errorf("got %d stall-wedge findings, want exactly 1", n)
	}
}

// TestClassifyNilReport is the trivial guard: a nil report yields no findings,
// never a panic.
func TestClassifyNilReport(t *testing.T) {
	if f := retro.Classify(nil, nil); f != nil {
		t.Errorf("Classify(nil) = %v, want nil", f)
	}
}

// --- test builders ---

func plan(id string, mut func(*retro.PlanRetro)) retro.PlanRetro {
	p := retro.PlanRetro{ID: id}
	if mut != nil {
		mut(&p)
	}
	return p
}

func report(plans ...retro.PlanRetro) *retro.Report {
	return &retro.Report{Plans: plans}
}

func reportMode(mode string, plans ...retro.PlanRetro) *retro.Report {
	return &retro.Report{BatchMode: mode, Plans: plans}
}

func reportBatch(totalUSD float64) *retro.Report {
	return &retro.Report{TotalUSD: totalUSD}
}

func hasKey(findings []retro.Finding, key string) bool {
	_, ok := findingByKey(findings, key)
	return ok
}

func findingByKey(findings []retro.Finding, key string) (retro.Finding, bool) {
	for _, f := range findings {
		if f.PatternKey == key {
			return f, true
		}
	}
	return retro.Finding{}, false
}

func countKey(findings []retro.Finding, key string) int {
	n := 0
	for _, f := range findings {
		if f.PatternKey == key {
			n++
		}
	}
	return n
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
