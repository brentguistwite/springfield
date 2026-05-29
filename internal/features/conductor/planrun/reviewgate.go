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
	Ctx               context.Context
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
	// TamperGuard, when non-nil, wraps BOTH the reviewer agent run AND the
	// fix-iteration agent run. The reviewer is read-only "by contract" but the
	// contract is only as strong as the underlying agent's compliance: under
	// cross-agent review (e.g. Codex reviewing a Claude-implemented plan) a
	// prompt-injected diff fragment could instruct the reviewer to write a
	// crafted .springfield/ state file before the fix runs, silently shifting
	// the baseline that the fix-iteration guard then snapshots. Wrapping the
	// reviewer call closes that window: any tamper from the reviewer is
	// detected and restored before the next iteration's snapshot is taken.
	TamperGuard TamperGuard
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

	ctx := in.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	var criteria []string
	for _, s := range in.PRD.UserStories {
		criteria = append(criteria, s.AcceptanceCriteria...)
	}

	reviewerAgents := in.ImplementerAgents
	if a := strings.TrimSpace(in.ReviewConfig.Agent); a != "" {
		reviewerAgents = []agents.ID{agents.ID(a)}
	}

	for round := 1; ; round++ {
		if err := ctx.Err(); err != nil {
			return reviewGateResult{Outcome: reviewErrored, Err: err}
		}
		diff, err := in.Git.Diff(in.WorktreeRoot, in.BaseRef)
		if err != nil {
			return reviewGateResult{Outcome: reviewErrored, Err: fmt.Errorf("compute review diff: %w", err)}
		}
		// Snapshot control plane before the reviewer runs. The reviewer is
		// nominally read-only, but under cross-agent review a prompt-injected
		// diff could coerce it into writing .springfield/ state. Detecting
		// before the fix-iteration's own snapshot prevents reviewer tamper
		// from silently becoming the new baseline.
		if in.TamperGuard != nil {
			if snapErr := in.TamperGuard.Snapshot(); snapErr != nil {
				return reviewGateResult{Outcome: reviewErrored, Err: fmt.Errorf("tamper guard snapshot (review %d): %w", round, snapErr)}
			}
		}
		rev := planreview.Review(ctx, planreview.ReviewInput{
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
		if in.TamperGuard != nil {
			tamperReason, detectErr := in.TamperGuard.Detect()
			if detectErr != nil {
				return reviewGateResult{Outcome: reviewErrored, Err: fmt.Errorf("tamper guard detect (review %d): %w", round, detectErr)}
			}
			if tamperReason != "" {
				_ = in.TamperGuard.Restore()
				return reviewGateResult{Outcome: reviewErrored, Err: fmt.Errorf("tamper-detected during review %d: %s", round, tamperReason)}
			}
		}
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

		// Snapshot control plane before the fix-iteration agent runs — the
		// implementer can absolutely touch .springfield/ and the same protection
		// the main story loop uses must apply here.
		if in.TamperGuard != nil {
			if snapErr := in.TamperGuard.Snapshot(); snapErr != nil {
				return reviewGateResult{Outcome: reviewErrored, Err: fmt.Errorf("tamper guard snapshot (review-fix %d): %w", round, snapErr)}
			}
		}
		fix := in.Runner.Run(ctx, coreruntime.Request{
			AgentIDs:          in.ImplementerAgents,
			Prompt:            fixPrompt,
			WorkDir:           in.WorktreeRoot,
			OnEvent:           in.OnEvent,
			ExecutionSettings: in.ExecutionSettings,
		})
		writeReviewEvidence(in.EvidenceDir, fmt.Sprintf("review-fix-%d", round), fixPrompt, string(fix.Agent), fix.Events, fix.Err)
		if in.TamperGuard != nil {
			tamperReason, detectErr := in.TamperGuard.Detect()
			if detectErr != nil {
				return reviewGateResult{Outcome: reviewErrored, Err: fmt.Errorf("tamper guard detect (review-fix %d): %w", round, detectErr)}
			}
			if tamperReason != "" {
				_ = in.TamperGuard.Restore()
				return reviewGateResult{Outcome: reviewErrored, Err: fmt.Errorf("tamper-detected during review-fix %d: %s", round, tamperReason)}
			}
		}
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
