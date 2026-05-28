package planrun

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"springfield/internal/core/agents"
	"springfield/internal/core/config"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor/planreview"
	"springfield/internal/features/execution"
	"springfield/internal/features/prd"
)

// reviewOutcome is the gate's three-way verdict. reviewPassed means the work
// satisfies the criteria; reviewNeedsHuman means the reviewer halted OR the
// fix-loop hit max_review_iterations; reviewErrored means an agent invocation
// itself failed.
type reviewOutcome int

const (
	reviewPassed reviewOutcome = iota
	reviewNeedsHuman
	reviewErrored
)

// reviewGateInput is everything the fix-loop needs. All dependencies (runner,
// git) are injected so the gate is unit-testable with fakes.
type reviewGateInput struct {
	Runner            AgentRunner
	Git               Git
	ImplementerAgents []agents.ID
	ExecutionSettings agents.ExecutionSettings
	ReviewConfig      config.ReviewConfig
	WorktreeRoot      string
	BaseRef           string
	PRD               prd.PRD
	ContextMD         string
	ProjectGuidance   string
	ProjectRoot       string
	EvidenceDir       string
	OnEvent           coreexec.EventHandler
}

type reviewGateResult struct {
	Outcome  reviewOutcome
	Findings string
	Err      error
}

// runReviewGate runs the review fix-loop with its OWN counter (independent of
// single_workstream_iterations). One round = one review + (on revise) one
// implementer fix-iteration. pass → reviewPassed; halt → reviewNeedsHuman;
// revise/verdict-less → loop until max_review_iterations, then reviewNeedsHuman.
// Any agent error → reviewErrored.
func runReviewGate(in reviewGateInput) reviewGateResult {
	max := in.ReviewConfig.MaxReviewIterationsOrDefault()

	var criteria []string
	for _, s := range in.PRD.UserStories {
		criteria = append(criteria, s.AcceptanceCriteria...)
	}

	reviewerAgents := in.ImplementerAgents
	if a := strings.TrimSpace(in.ReviewConfig.Agent); a != "" {
		reviewerAgents = []agents.ID{agents.ID(a)}
	}

	for round := 1; ; round++ {
		diff, err := in.Git.Diff(in.WorktreeRoot, in.BaseRef)
		if err != nil {
			return reviewGateResult{Outcome: reviewErrored, Err: fmt.Errorf("compute review diff: %w", err)}
		}
		rev := planreview.Review(context.Background(), planreview.ReviewInput{
			Runner:            in.Runner,
			AgentIDs:          reviewerAgents,
			ExecutionSettings: in.ExecutionSettings,
			WorkDir:           in.WorktreeRoot,
			Diff:              diff,
			Criteria:          criteria,
			CustomPrompt:      in.ReviewConfig.Prompt,
			OnEvent:           in.OnEvent,
		})
		writeReviewEvidence(in.EvidenceDir, fmt.Sprintf("review-iter-%d", round),
			planreview.BuildReviewPrompt(diff, criteria, in.ReviewConfig.Prompt), string(rev.Agent), rev.Events, rev.Err)
		if rev.Err != nil {
			return reviewGateResult{Outcome: reviewErrored, Err: fmt.Errorf("reviewer agent failed: %w", rev.Err)}
		}

		if rev.Found && rev.Verdict.Class == planreview.VerdictPass {
			return reviewGateResult{Outcome: reviewPassed}
		}
		if rev.Found && rev.Verdict.Class == planreview.VerdictHalt {
			return reviewGateResult{Outcome: reviewNeedsHuman, Findings: rev.Verdict.Findings}
		}

		// revise OR verdict-less → fixable; consume this round.
		findings := rev.Verdict.Findings
		if !rev.Found {
			findings = "The previous review produced no clear verdict. Re-examine the work for correctness, completeness, and test coverage, and harden anything questionable."
		}
		if round >= max {
			return reviewGateResult{Outcome: reviewNeedsHuman, Findings: findings}
		}

		fixPrompt, err := BuildReviewFixPrompt(in.PRD, in.ContextMD, in.ProjectGuidance, findings, in.ProjectRoot)
		if err != nil {
			return reviewGateResult{Outcome: reviewErrored, Err: fmt.Errorf("build review-fix prompt: %w", err)}
		}
		fix := in.Runner.Run(context.Background(), coreruntime.Request{
			AgentIDs:          in.ImplementerAgents,
			Prompt:            fixPrompt,
			WorkDir:           in.WorktreeRoot,
			OnEvent:           in.OnEvent,
			ExecutionSettings: in.ExecutionSettings,
		})
		writeReviewEvidence(in.EvidenceDir, fmt.Sprintf("review-fix-%d", round), fixPrompt, string(fix.Agent), fix.Events, fix.Err)
		if fix.Err != nil {
			return reviewGateResult{Outcome: reviewErrored, Err: fmt.Errorf("review-fix iteration failed: %w", fix.Err)}
		}
	}
}

// writeReviewEvidence is best-effort (matches the runner's per-iteration
// evidence: a failed write warns but never aborts the run).
func writeReviewEvidence(evidenceDir, label, prompt, agentID string, events []coreexec.Event, runErr error) {
	if evidenceDir == "" {
		return
	}
	_ = execution.WriteEvidence(filepath.Join(evidenceDir, label), execution.EvidenceSnapshot{
		AgentID: agentID,
		Prompt:  prompt,
		Events:  events,
		Err:     runErr,
	})
}
