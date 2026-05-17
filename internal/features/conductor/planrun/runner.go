package planrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"springfield/internal/core/agents"
	"springfield/internal/core/config"
	"springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
	"springfield/internal/features/cost"
	"springfield/internal/features/execution"
	"springfield/internal/features/prd"
)

// AgentRunner is the runtime boundary planrun depends on. The shared
// coreruntime.Runner satisfies it directly.
type AgentRunner interface {
	Run(ctx context.Context, req coreruntime.Request) coreruntime.Result
}

// TamperGuard wraps an agent invocation with control-plane integrity checks.
// Snapshot is called before the agent runs to capture current state. Detect is
// called after the agent returns to check for mutations. Restore reverts any
// mutations back to the snapshotted state.
//
// A nil TamperGuard disables tamper detection (used in tests that don't need it).
type TamperGuard interface {
	Snapshot() error
	Detect() (reason string, err error)
	Restore() error
}

// EvidenceRoot returns the per-plan evidence directory under ControlRoot.
// The directory is plan-key namespaced so concurrent plan units cannot stomp
// each other's evidence and so resume can find the prior attempt's bytes.
func EvidenceRoot(controlRoot, planKey string) string {
	return filepath.Join(controlRoot, ".springfield", "execution", "plans", planKey, "evidence")
}

// SinglePlanInput collects everything needed to execute one registered plan
// in its isolated worktree. The runtime runner is injected so tests can
// substitute a fake; the project carries the canonical config + state and is
// the durable store for truthful per-plan metadata.
type SinglePlanInput struct {
	Project           *conductor.Project
	ControlRoot       string
	WorktreeBase      string
	AgentIDs          []agents.ID
	ExecutionSettings agents.ExecutionSettings
	Runner            AgentRunner
	Manager           *Manager
	OnEvent           exec.EventHandler
	// Progress receives short human-readable lifecycle lines; nil discards.
	Progress io.Writer
	// Now is injected for deterministic state timestamps in tests; nil
	// defaults to time.Now.
	Now func() time.Time
	// EnforceProtectedBase refuses to run a plan that would ff-merge into a
	// protected branch (see [ProtectedBases]). Threaded through to
	// PrepareInput; cmd/start enables this by default.
	EnforceProtectedBase bool
	// ProjectRoot is the project's config root used to resolve operator-override
	// prompt templates. Caller passes the same path used for config.LoadFrom.
	// MUST NOT fall back to os.Getwd inside BuildPromptForPlan.
	ProjectRoot string
	// TargetPlanID pins which plan to dispatch. When non-empty, SinglePlan
	// uses this ID instead of next[0] from the schedule, provided the target
	// is still eligible (present in NextPlans). Callers that filter by batch
	// membership set this field so outside-batch plans don't accidentally run.
	// Zero value falls back to next[0] for legacy callers.
	TargetPlanID string
	// TamperGuard, when non-nil, is called around each agent invocation to detect
	// and recover control-plane mutations. Snapshot is called before Run, Detect
	// after. On detected tamper the plan is marked failed and the loop aborts.
	// Nil disables tamper detection.
	TamperGuard TamperGuard
	// ReviewConfig is the resolved [review] block from springfield.local.toml.
	// Zero value (Enabled=false) disables review; the per-plan prd.PRD.Review
	// flag can still override per config.ReviewEnabledForPlan.
	ReviewConfig config.ReviewConfig
	// Ctx, when non-nil, is the parent context for in-loop agent runs that
	// participate in cooperative cancellation. The legacy story-iteration
	// dispatch sites use context.Background (no cancellation surface); the
	// review fix-loop honours Ctx so a SIGINT mid-review unwinds promptly.
	// nil → context.Background, preserving prior behavior.
	Ctx context.Context
	// MaxTurnsPerIteration caps the agent turns a single PRD iteration may
	// consume before the run is failed with [TurnCapExceededReason] — the B2
	// thrash circuit-breaker. Callers pass config.Config.MaxTurnsPerIteration()
	// (which defaults to 40 when the springfield.toml key is omitted). The zero
	// value disables the check, so tests and legacy callers that leave it unset
	// keep their prior behavior.
	//
	// PRD plans only. Legacy `.md` plans silently ignore this field because
	// the legacy single-shot path has no completion oracle a WorkCompleteCheck
	// closure could consult — forwarding the cap there would demote
	// legitimate long-running runs to thrash. See [singlePlanLegacy]. The
	// silent-ignore behavior is surfaced to operators as a progress-line
	// warning when the field is non-zero on a legacy dispatch.
	MaxTurnsPerIteration int
	// CostCapUSD bounds total spend across the live batch's evidence
	// directories. When > 0, the iteration loop recomputes cost.ComputeRollup
	// after each iteration's cost.json is written; if TotalUSD >= CostCapUSD,
	// the loop breaks with Reason "cost-capped" so the caller (runBatch) can
	// abort the batch without dispatching further plans. Zero disables.
	CostCapUSD float64
}

// SinglePlanResult summarizes the outcome.
type SinglePlanResult struct {
	PlanID       string
	Reason       string
	Reused       bool
	Context      Context
	EvidencePath string
	Agent        string
	Status       conductor.PlanStatus
	Err          error
	// CostCapped is true when the iteration loop broke because the rollup
	// crossed CostCapUSD. The caller must NOT treat this as a plan failure
	// (Err stays nil); it is a batch-level pause that needs CostCapped
	// status persisted on Run.
	CostCapped bool
	// SpendUSD reports the rollup TotalUSD at the moment the cap fired; zero
	// otherwise. Surfacing it on the result avoids a second ComputeRollup
	// call in the caller for the cap-message dollar figure.
	SpendUSD float64
}

// iterationSummary is written once at loop exit to evidenceDir/summary.json.
type iterationSummary struct {
	IterationCount int    `json:"iteration_count"`
	TerminalStatus string `json:"terminal_status"`
	ExitReason     string `json:"exit_reason"`
}

// SinglePlan picks the next eligible plan unit, runs preflight, prepares the
// worktree, then runs the bounded iteration loop: pick story → build prompt →
// dispatch agent → scan markers → mark passed → repeat until all stories pass,
// <promise>COMPLETE</promise> is validated, or the iteration cap is reached.
// State is saved on every terminal transition so a crash mid-run still leaves an honest record.
func SinglePlan(in SinglePlanInput) SinglePlanResult {
	if in.Project == nil || in.Project.Config == nil {
		return SinglePlanResult{Err: fmt.Errorf("project is not loaded")}
	}
	if in.Manager == nil {
		in.Manager = NewManager()
	}
	now := in.Now
	if now == nil {
		now = time.Now
	}

	var planID string
	if in.TargetPlanID != "" {
		// TargetPlanID bypasses the global schedule order. Callers (batch loop)
		// own phase ordering; SinglePlan only needs to validate that the target
		// exists and is not already terminal.
		unit, ok := in.Project.PlanUnitByID(in.TargetPlanID)
		_ = unit // checked again below after planID is set
		if !ok {
			return SinglePlanResult{
				PlanID: in.TargetPlanID,
				Reason: "no-eligible-plan",
			}
		}
		// Reject targets that are already in a terminal state.
		if ps := in.Project.State.Plans[in.TargetPlanID]; ps != nil &&
			(ps.Status == conductor.StatusCompleted || ps.Status == conductor.StatusFailed) &&
			ps.IsIntegrated() {
			return SinglePlanResult{
				PlanID: in.TargetPlanID,
				Reason: "no-eligible-plan",
			}
		}
		planID = in.TargetPlanID
	} else {
		schedule := conductor.BuildSchedule(in.Project.Config)
		next := schedule.NextPlans(in.Project.State)
		if len(next) == 0 {
			return SinglePlanResult{Reason: "no-eligible-plan"}
		}
		planID = next[0]
	}
	unit, ok := in.Project.PlanUnitByID(planID)
	if !ok {
		return SinglePlanResult{PlanID: planID, Err: fmt.Errorf("plan %q is scheduled but not registered", planID)}
	}

	progress(in.Progress, "plan %s: preflight\n", planID)

	prior := in.Project.State.Plans[planID]
	decision, err := in.Manager.Prepare(PrepareInput{
		ControlRoot:          in.ControlRoot,
		WorktreeBase:         in.WorktreeBase,
		Unit:                 unit,
		PriorState:           prior,
		AllStates:            in.Project.State.Plans,
		EnforceProtectedBase: in.EnforceProtectedBase,
	})
	if err != nil {
		tag := "preflight-error"
		if pe := AsPreflight(err); pe != nil {
			tag = pe.Tag
		}
		recordPreflightFailure(in.Project, planID, tag, err.Error(), now())
		_ = in.Project.SaveState()
		return SinglePlanResult{PlanID: planID, Reason: tag, Err: err}
	}

	ctx := decision.Context
	if !decision.Reuse {
		progress(in.Progress, "plan %s: creating worktree at %s (branch %s, base %s)\n",
			planID, ctx.WorktreeRoot, ctx.Branch, ctx.BaseRef)
		if err := in.Manager.CreateWorktree(ctx); err != nil {
			recordPreflightFailure(in.Project, planID, "worktree-create-failed", err.Error(), now())
			_ = in.Project.SaveState()
			return SinglePlanResult{PlanID: planID, Reason: "worktree-create-failed", Context: ctx, Err: err}
		}
	} else {
		progress(in.Progress, "plan %s: reusing worktree at %s (%s)\n", planID, ctx.WorktreeRoot, decision.Reason)
	}

	// Mark running with truthful worktree metadata before dispatch so a
	// crash leaves an honest state file.
	startState := &conductor.PlanState{
		Status:       conductor.StatusRunning,
		Attempts:     attemptsOf(prior) + 1,
		StartedAt:    now(),
		WorktreePath: ctx.WorktreeRoot,
		Branch:       ctx.Branch,
		BaseRef:      ctx.BaseRef,
		BaseHead:     ctx.BaseHead,
		InputDigest:  decision.InputDigest,
		ExitReason:   "",
		// Preserve previously-known agent/evidence pointers across attempts.
		Agent:        agentOf(prior),
		EvidencePath: evidenceOf(prior),
	}

	// Detect PRD vs legacy plan by path suffix. PRD plans have prd.json paths;
	// legacy plans are .md files registered before Phase 3.
	unitPath := filepath.FromSlash(unit.Path)
	isPRDPlan := filepath.Base(unitPath) == "prd.json"

	if !isPRDPlan {
		return singlePlanLegacy(in, planID, unit, ctx, decision, startState, now)
	}

	// PRD plan path: save running state before dispatching the iteration loop.
	in.Project.State.Plans[planID] = startState
	if err := in.Project.SaveState(); err != nil {
		return SinglePlanResult{PlanID: planID, Reason: "save-state-failed", Context: ctx, Err: err}
	}

	// Derive plan-level paths. prdPath is the sole writer for story pass state.
	prdPath := filepath.Join(in.ControlRoot, unitPath)
	progressPath := filepath.Join(filepath.Dir(prdPath), "progress.md")
	contextPath := filepath.Join(filepath.Dir(prdPath), "context.md")

	currentPRD, err := prd.ParseFile(prdPath)
	if err != nil {
		recordPreflightFailure(in.Project, planID, "prd-load-failed", err.Error(), now())
		_ = in.Project.SaveState()
		return SinglePlanResult{PlanID: planID, Reason: "prd-load-failed", Context: ctx, Err: err}
	}

	// Runtime guard: reject zero-story PRDs. The validator in prd.Validate
	// already rejects them at compile time; this guard catches files that were
	// hand-edited after compilation and bypassed the validator.
	if len(currentPRD.UserStories) == 0 {
		zeroErr := fmt.Errorf("prd has zero user stories — manual edit detected")
		recordPreflightFailure(in.Project, planID, "prd-zero-stories", zeroErr.Error(), now())
		_ = in.Project.SaveState()
		return SinglePlanResult{PlanID: planID, Reason: "prd-zero-stories", Context: ctx, Status: conductor.StatusFailed, Err: zeroErr}
	}

	contextMDBytes, _ := os.ReadFile(contextPath) // not-exist → empty is fine
	projectGuidance := loadProjectGuidance(in.ControlRoot)

	evidenceDir := EvidenceRoot(in.ControlRoot, ctx.PlanKey)
	iterCap := in.Project.Config.SingleWorkstreamIterations
	if iterCap <= 0 {
		iterCap = 50
	}

	var (
		finalRunErr       error
		completedNormally bool
		needsHuman        bool
		exitReason        = "completed"
		lastAgent         agents.ID
		iterCount         int
		costCapped        bool
		costCapSpend      float64
	)

	// finishWithReview gates plan completion on the pre-merge review fix-loop.
	// Called at BOTH completion sites (top-of-loop short-circuit AND post-COMPLETE
	// check) so a needs-human retry that re-enters with all stories already passed
	// re-reviews instead of merging unreviewed.
	finishWithReview := func() {
		if !config.ReviewEnabledForPlan(in.ReviewConfig, currentPRD.Review) {
			completedNormally = true
			return
		}
		pr := in.ProjectRoot
		if pr == "" {
			pr = in.ControlRoot
		}
		gate := runReviewGate(reviewGateInput{
			Runner:            in.Runner,
			Git:               in.Manager.Git,
			ImplementerAgents: in.AgentIDs,
			ExecutionSettings: in.ExecutionSettings,
			ReviewConfig:      in.ReviewConfig,
			WorktreeRoot:      ctx.WorktreeRoot,
			BaseRef:           ctx.BaseRef,
			PRD:               currentPRD,
			ContextMD:         string(contextMDBytes),
			ProjectGuidance:   projectGuidance,
			ProjectRoot:       pr,
			EvidenceDir:       evidenceDir,
			OnEvent:           in.OnEvent,
			TamperGuard:       in.TamperGuard,
			Ctx:               in.Ctx,
		})
		switch gate.Outcome {
		case reviewPassed:
			completedNormally = true
		case reviewNeedsHuman:
			needsHuman = true
			exitReason = "review-needs-human"
			if excerpt := truncateForError(gate.Findings, 200); excerpt != "" {
				finalRunErr = fmt.Errorf("pre-merge review halted: %s (full findings in evidence)", excerpt)
			} else {
				// Reviewer emitted halt with no prose (rare but possible — the
				// verdict marker line itself is stripped from Findings). Avoid
				// the awkward "halted:  (…)" double-space.
				finalRunErr = fmt.Errorf("pre-merge review halted (full findings in evidence)")
			}
		case reviewErrored:
			finalRunErr = gate.Err
			exitReason = "review-errored"
		}
	}

	for iter := 1; iter <= iterCap; iter++ {
		story, pickStatus := NextStory(currentPRD)
		if pickStatus == PickAllPassed {
			// All stories already passed: either a fresh final iteration, OR a
			// resume/retry of already-complete work (e.g. a needs-human retry).
			// Gate on review at this site too — otherwise the retry would merge
			// unreviewed.
			finishWithReview()
			break
		}
		if pickStatus == PickBlocked {
			// Dep graph is unsatisfiable (cycle or unresolvable deps). Fail the plan.
			finalRunErr = fmt.Errorf("story dependency graph blocked: no eligible story")
			exitReason = "story dependency graph blocked: no eligible story"
			break
		}

		projectRoot := in.ProjectRoot
		if projectRoot == "" {
			projectRoot = in.ControlRoot
		}
		prompt, err := BuildPromptForPlan(currentPRD, string(contextMDBytes), projectGuidance, story, projectRoot)
		if err != nil {
			finalRunErr = fmt.Errorf("build prompt iter %d: %w", iter, err)
			exitReason = "prompt-build-failed"
			break
		}

		iterCount++
		progress(in.Progress, "plan %s: iteration %d (story=%s)\n", planID, iter, story.ID)
		_ = AppendProgress(progressPath, fmt.Sprintf("%s iteration %d start (story=%s)",
			now().UTC().Format(time.RFC3339), iter, story.ID))

		// Snapshot control-plane state before dispatching the agent.
		if in.TamperGuard != nil {
			if snapErr := in.TamperGuard.Snapshot(); snapErr != nil {
				finalRunErr = fmt.Errorf("tamper guard snapshot failed: %w", snapErr)
				exitReason = "tamper-guard-snapshot-failed"
				break
			}
		}

		// Capture the per-iteration scoping (currentPRD + target story) the
		// runtime needs to ask "did this run legitimately complete the
		// iteration?". The closure runs synchronously inside Runner.Run
		// (between exec returning and ClassifyError), so the values it reads
		// can never race with the post-Run MarkPassed loop below.
		iterStory := story
		workCompleteCheck := func(events []exec.Event) bool {
			return iterationWorkComplete(events, currentPRD, iterStory.ID)
		}
		result := in.Runner.Run(context.Background(), coreruntime.Request{
			AgentIDs:             in.AgentIDs,
			Prompt:               prompt,
			WorkDir:              ctx.WorktreeRoot,
			OnEvent:              in.OnEvent,
			ExecutionSettings:    in.ExecutionSettings,
			MaxTurnsPerIteration: in.MaxTurnsPerIteration,
			WorkCompleteCheck:    workCompleteCheck,
		})
		lastAgent = result.Agent

		// Detect and recover any control-plane tamper by the agent.
		if in.TamperGuard != nil {
			tamperReason, detectErr := in.TamperGuard.Detect()
			if detectErr != nil {
				finalRunErr = fmt.Errorf("tamper guard detect failed: %w", detectErr)
				exitReason = "tamper-guard-detect-failed"
				break
			}
			if tamperReason != "" {
				_ = in.TamperGuard.Restore()
				finalRunErr = fmt.Errorf("tamper-detected: %s", tamperReason)
				exitReason = fmt.Sprintf("tamper-detected: %s", tamperReason)
				break
			}
		}

		// Write per-iteration evidence.
		iterDir := filepath.Join(evidenceDir, fmt.Sprintf("iter-%d", iter))
		iterRunErr := errorFromResult(result)
		snap := execution.EvidenceSnapshot{
			AgentID:   string(result.Agent),
			Model:     modelForAgent(result.Agent, in.ExecutionSettings),
			ExitCode:  result.ExitCode,
			Prompt:    prompt,
			Events:    result.Events,
			StartedAt: result.StartedAt,
			EndedAt:   result.EndedAt,
			Err:       iterRunErr,
		}
		if err := execution.WriteEvidence(iterDir, snap); err != nil {
			fmt.Fprintf(os.Stderr, "warning: write evidence iter %d for plan %s: %v\n", iter, planID, err)
		}
		capture := extractCost(result.Agent, result.Events, snap.Model, now())
		if err := execution.WriteCost(iterDir, capture); err != nil {
			fmt.Fprintf(os.Stderr, "warning: write cost iter %d for plan %s: %v\n", iter, planID, err)
		}

		// Story-pass scan happens BEFORE the cost-cap check so any progress
		// the agent earned during the capping iteration is durably persisted
		// to the PRD before the loop breaks. Otherwise the agent would have
		// to redo the same story on resume, wasting both cost and time.
		passedIDs, complete := ScanMarkers(result.Events)
		honoredPasses := 0
		for _, sid := range passedIDs {
			// Only honour the marker if it matches the current iteration's target story.
			// Agents may emit pass markers for wrong stories; accepting them would
			// permanently skip future stories without them being worked on.
			if sid != story.ID {
				_ = AppendProgress(progressPath, fmt.Sprintf("%s WARN: agent emitted <story-pass>%s</story-pass> but iteration target was %s; ignoring marker",
					now().UTC().Format(time.RFC3339), sid, story.ID))
				continue
			}
			if err := MarkPassed(prdPath, sid); err != nil {
				finalRunErr = fmt.Errorf("MarkPassed failed: %w", err)
				exitReason = fmt.Sprintf("MarkPassed failed: %v", err)
				break
			}
			honoredPasses++
			// Reflect into in-memory copy.
			for i := range currentPRD.UserStories {
				if currentPRD.UserStories[i].ID == sid {
					currentPRD.UserStories[i].Passes = true
				}
			}
			_ = AppendProgress(progressPath, fmt.Sprintf("%s [%s] passed", now().UTC().Format(time.RFC3339), sid))
		}
		if finalRunErr != nil {
			break
		}

		// Cost-cap check fires after evidence + story-pass are durably
		// persisted so a runaway plan with many iterations cannot blow past
		// the cap. Mid-iteration: the iteration that crossed the threshold
		// is allowed to finish (its evidence is already on disk); the NEXT
		// iteration is what doesn't dispatch. Uses >= so total == cap fires
		// (boundary spec).
		if in.CostCapUSD > 0 {
			r, rollupErr := cost.ComputeRollup(in.ControlRoot, "")
			if rollupErr != nil {
				// Rollup error is conservatively treated as cap-not-hit
				// rather than failing the run; the warning lands in stderr
				// so the operator can investigate. Failing the plan over
				// a transient FS hiccup would be a worse outcome than
				// letting one more iteration run.
				fmt.Fprintf(os.Stderr, "warning: compute rollup for cost-cap check (plan %s, iter %d): %v\n", planID, iter, rollupErr)
			} else if r.TotalUSD >= in.CostCapUSD {
				costCapped = true
				costCapSpend = r.TotalUSD
				exitReason = fmt.Sprintf("cost-capped at $%.2f (cap $%.2f)", r.TotalUSD, in.CostCapUSD)
				_ = AppendProgress(progressPath, fmt.Sprintf("%s cost-capped at $%.2f after iteration %d", now().UTC().Format(time.RFC3339), r.TotalUSD, iter))
				break
			} else if r.SkippedFiles > 0 {
				// Cap not tripped, but some evidence was unreadable. Surface
				// once per iteration so the operator can investigate before
				// the silent under-count actually matters.
				fmt.Fprintf(os.Stderr, "warning: cost-cap check (plan %s, iter %d): %d cost.json file(s) unreadable; rollup may under-count\n", planID, iter, r.SkippedFiles)
			}
		}

		// Report passes actually honored, not raw markers scanned — off-target
		// markers are logged as WARN above and must not inflate this count.
		_ = AppendProgress(progressPath, fmt.Sprintf("%s iteration %d completed (passed=%d complete=%t)",
			now().UTC().Format(time.RFC3339), iter, honoredPasses, complete))

		// COMPLETE is "honored" only when the marker was emitted AND every story
		// now passes — Springfield's own record of completion, distinct from a
		// raw marker on stdout. Computed before the exit-code branch so a
		// post-completion crash can be told apart from a genuine mid-work crash.
		completeHonored := false
		if complete {
			if _, stillStatus := NextStory(currentPRD); stillStatus == PickAllPassed {
				completeHonored = true
			}
		}

		if iterRunErr != nil {
			// The agent process exited non-zero. Judge by work, not exit code:
			// if the plan's work was provably finished before the crash, treat
			// the crash as post-success teardown noise rather than a failure.
			worktreeHead, _ := in.Manager.Git.Head(ctx.WorktreeRoot)
			if workCompletedBeforeCrash(completeHonored, currentPRD, ctx.BaseHead, worktreeHead) {
				_ = AppendProgress(progressPath, fmt.Sprintf("%s post-completion crash ignored (work complete before exit): %v",
					now().UTC().Format(time.RFC3339), iterRunErr))
				finishWithReview()
				break
			}
			finalRunErr = iterRunErr
			// Preserve the structured turn-cap tag when the failure bubbled
			// up from the runtime layer's circuit-breaker. The runtime
			// synthesizes a retryable error tagged [TurnCapExceededReason]
			// when a clean-exiting agent burns more turns than the cap; if
			// every agent in the chain hit the same wall, that's the honest
			// reason — surfacing "agent-failed" instead would lose the
			// operator-visible signal that thrash, not a real error, broke
			// the iteration.
			if strings.Contains(iterRunErr.Error(), TurnCapExceededReason) {
				exitReason = TurnCapExceededReason
				_ = AppendProgress(progressPath, fmt.Sprintf("%s %s",
					now().UTC().Format(time.RFC3339), iterRunErr.Error()))
			} else {
				exitReason = "agent-failed"
			}
			break
		}

		if completeHonored {
			finishWithReview()
			break
		}

		if complete {
			// Premature COMPLETE — log warning, continue.
			_ = AppendProgress(progressPath, fmt.Sprintf("%s WARN: COMPLETE emitted but stories remain pending; ignoring marker",
				now().UTC().Format(time.RFC3339)))
		}
	}

	// After loop: an incomplete run that didn't halt for human review and
	// wasn't cost-capped means the iteration cap was hit — a real failure.
	// needsHuman (review halt) and costCapped (resumable batch pause) are
	// both non-failure terminal states and must be excluded here.
	if !completedNormally && !needsHuman && finalRunErr == nil && !costCapped {
		finalRunErr = fmt.Errorf("iteration cap reached without completion marker")
		exitReason = "iteration cap reached without completion marker"
	}

	// Write summary.json once at loop exit.
	terminalStatus := "completed"
	switch { // needsHuman MUST be evaluated before finalRunErr != nil — the
	// review-halt path sets BOTH (a non-nil err halts the batch loop) and is
	// status needs-human, not failed.
	case needsHuman:
		terminalStatus = "needs-human"
	case finalRunErr != nil:
		terminalStatus = "failed"
	}
	summary := iterationSummary{
		IterationCount: iterCount,
		TerminalStatus: terminalStatus,
		ExitReason:     exitReason,
	}
	if summaryData, err := json.MarshalIndent(summary, "", "  "); err == nil {
		summaryPath := filepath.Join(evidenceDir, "summary.json")
		if err := os.MkdirAll(evidenceDir, 0o755); err == nil {
			_ = os.WriteFile(summaryPath, append(summaryData, '\n'), 0o644)
		}
	}

	// Terminal state construction — same path for both success conditions.
	finalStatus := conductor.StatusCompleted
	switch { // needsHuman MUST be evaluated before finalRunErr != nil — see
	// matching switch above for the summary writer.
	case needsHuman:
		finalStatus = conductor.StatusNeedsHuman
		exitReason = terminalExitReason(exitReason, finalRunErr)
	case finalRunErr != nil:
		finalStatus = conductor.StatusFailed
		exitReason = terminalExitReason(exitReason, finalRunErr)
	}
	if costCapped {
		// Cost-cap is neither success nor failure: the plan paused
		// mid-flight, resumable when the operator passes a higher cap.
		// Reuse StatusInterrupted (also used for ctx-cancel) so the
		// plan does NOT enter merge integration and IsIntegrated stays
		// false until the next start advances it.
		finalStatus = conductor.StatusInterrupted
	}
	errOut := ""
	if finalRunErr != nil {
		errOut = finalRunErr.Error()
	}

	endState := &conductor.PlanState{
		Status:       finalStatus,
		Error:        errOut,
		Agent:        string(lastAgent),
		EvidencePath: evidenceDir,
		Attempts:     startState.Attempts,
		StartedAt:    startState.StartedAt,
		EndedAt:      now(),
		WorktreePath: ctx.WorktreeRoot,
		Branch:       ctx.Branch,
		BaseRef:      ctx.BaseRef,
		BaseHead:     ctx.BaseHead,
		InputDigest:  decision.InputDigest,
		ExitReason:   exitReason,
	}
	// On successful execution, mark merge integration as pending before persisting.
	if finalStatus == conductor.StatusCompleted {
		endState.Merge = &conductor.MergeOutcome{
			Status:      conductor.MergePending,
			Reason:      "awaiting-merge-integration",
			AttemptedAt: now(),
		}
		if planHead, err := in.Manager.Git.Head(ctx.WorktreeRoot); err == nil {
			endState.PlanHead = planHead
		}
	}
	in.Project.State.Plans[planID] = endState
	saveErr := in.Project.SaveState()

	resultReason := decision.Reason
	if finalRunErr != nil {
		resultReason = exitReason
	}
	if costCapped {
		// cost-cap pauses the batch but is NOT a plan failure; keep Err nil
		// so the caller sees a clean status and the cost-cap flag drives
		// the batch-level abort decision.
		resultReason = exitReason
	}
	out := SinglePlanResult{
		PlanID:       planID,
		Reason:       resultReason,
		Reused:       decision.Reuse,
		Context:      ctx,
		EvidencePath: evidenceDir,
		Agent:        string(lastAgent),
		Status:       finalStatus,
		Err:          finalRunErr,
		CostCapped:   costCapped,
		SpendUSD:     costCapSpend,
	}
	// SaveState failures must never be silent.
	switch {
	case finalRunErr != nil && saveErr != nil:
		out.Err = errors.Join(finalRunErr, fmt.Errorf("save state: %w", saveErr))
		out.Reason = "agent-failed-state-save-failed"
	case finalRunErr == nil && saveErr != nil:
		out.Err = fmt.Errorf("save state: %w", saveErr)
		out.Reason = "state-save-failed"
		out.Status = conductor.StatusFailed
	}
	switch {
	case out.Err == nil:
		progress(in.Progress, "plan %s: completed\n", planID)
	case finalRunErr != nil && saveErr != nil:
		progress(in.Progress, "plan %s: failed — agent: %s; state save also failed: %v\n", planID, finalRunErr.Error(), saveErr)
	case finalRunErr != nil:
		progress(in.Progress, "plan %s: failed — %s\n", planID, finalRunErr.Error())
	default:
		progress(in.Progress, "plan %s: state save failed — %v (agent succeeded but on-disk state may be stale)\n", planID, saveErr)
	}
	return out
}

// singlePlanLegacy handles the old single-shot dispatch for plans that
// use legacy .md paths (pre-Phase-3 registrations). The PRD iteration loop
// is only engaged for plans whose unit.Path ends in "prd.json".
func singlePlanLegacy(in SinglePlanInput, planID string, unit conductor.PlanUnit,
	ctx Context, decision PrepareDecision, startState *conductor.PlanState, now func() time.Time,
) SinglePlanResult {
	// Loudly warn when review is enabled but the plan is legacy-format. The
	// pre-merge review gate only runs on PRD-format plans (it lives inside the
	// PRD iteration loop, after PickAllPassed); a legacy `.md` plan that
	// completes with [review].enabled=true still merges unreviewed. Operators
	// migrating to PRD format need to know exactly which plans bypass the gate.
	if in.ReviewConfig.Enabled {
		progress(in.Progress, "plan %s: WARNING review.enabled=true but plan is legacy .md format; pre-merge review gate is PRD-only and will NOT run for this plan\n", planID)
	}
	// Same warning shape for the turn cap: cmd/start passes the configured
	// MaxTurnsPerIteration to every plan, but singlePlanLegacy intentionally
	// does NOT forward it (legacy has no completion oracle — see the Run
	// call site below). Operators who configure max_turns_per_iteration
	// expecting uniform thrash protection across plan formats need to see
	// the asymmetry surfaced, not buried in a code comment.
	if in.MaxTurnsPerIteration > 0 {
		progress(in.Progress, "plan %s: WARNING max_turns_per_iteration=%d configured but plan is legacy .md format; turn-cap circuit-breaker is PRD-only and will NOT apply to this plan\n", planID, in.MaxTurnsPerIteration)
	}

	// Write running state before dispatch (same as PRD path).
	in.Project.State.Plans[planID] = startState
	if err := in.Project.SaveState(); err != nil {
		return SinglePlanResult{PlanID: planID, Reason: "save-state-failed", Context: ctx, Err: err}
	}

	prompt, err := buildPrompt(in.ControlRoot, unit)
	if err != nil {
		recordPreflightFailure(in.Project, planID, "prompt-build-failed", err.Error(), now())
		_ = in.Project.SaveState()
		return SinglePlanResult{PlanID: planID, Reason: "prompt-build-failed", Context: ctx, Err: err}
	}

	// Snapshot control-plane state before dispatching the agent.
	if in.TamperGuard != nil {
		if snapErr := in.TamperGuard.Snapshot(); snapErr != nil {
			recordPreflightFailure(in.Project, planID, "tamper-guard-snapshot-failed", snapErr.Error(), now())
			_ = in.Project.SaveState()
			return SinglePlanResult{PlanID: planID, Reason: "tamper-guard-snapshot-failed", Context: ctx, Err: snapErr}
		}
	}

	progress(in.Progress, "plan %s: dispatching agent (workdir %s)\n", planID, ctx.WorktreeRoot)
	// Turn-cap is deliberately NOT forwarded on the legacy path. Legacy
	// plans are single-shot dispatches with no per-story pass markers, so
	// there is no completion oracle a [WorkCompleteCheck] closure could
	// consult — every clean exit would default to "thrash" and a 200-turn
	// legitimately-completing legacy run would be demoted to a retryable
	// failure, fall through to the next agent on an already-mutated
	// worktree, and either re-do the work or fail outright. Adversarial
	// review round 2 caught this regression. The cap stays PRD-only until
	// a reliable legacy completion oracle exists. "Worktree advanced past
	// baseHead" alone is NOT sufficient — an agent that committed once at
	// turn 5 and then thrashed for 195 turns would satisfy it. A real
	// oracle needs a semantic success signal (e.g. an explicit
	// `<promise>COMPLETE</promise>` honored against project-defined exit
	// criteria), which legacy `.md` plans do not carry.
	result := in.Runner.Run(context.Background(), coreruntime.Request{
		AgentIDs:          in.AgentIDs,
		Prompt:            prompt,
		WorkDir:           ctx.WorktreeRoot,
		OnEvent:           in.OnEvent,
		ExecutionSettings: in.ExecutionSettings,
	})

	// Detect and recover any control-plane tamper by the agent.
	if in.TamperGuard != nil {
		tamperReason, detectErr := in.TamperGuard.Detect()
		if detectErr != nil {
			err := fmt.Errorf("tamper guard detect failed: %w", detectErr)
			recordPreflightFailure(in.Project, planID, "tamper-guard-detect-failed", err.Error(), now())
			_ = in.Project.SaveState()
			return SinglePlanResult{PlanID: planID, Reason: "tamper-guard-detect-failed", Context: ctx, Err: err}
		}
		if tamperReason != "" {
			_ = in.TamperGuard.Restore()
			tamperErr := fmt.Errorf("tamper-detected: %s", tamperReason)
			tamperTag := fmt.Sprintf("tamper-detected: %s", tamperReason)
			recordPreflightFailure(in.Project, planID, tamperTag, tamperErr.Error(), now())
			_ = in.Project.SaveState()
			return SinglePlanResult{PlanID: planID, Reason: tamperTag, Context: ctx, Status: conductor.StatusFailed, Err: tamperErr}
		}
	}

	evidenceDir := EvidenceRoot(in.ControlRoot, ctx.PlanKey)
	runErr := errorFromResult(result)
	snap := execution.EvidenceSnapshot{
		AgentID:   string(result.Agent),
		Model:     modelForAgent(result.Agent, in.ExecutionSettings),
		ExitCode:  result.ExitCode,
		Prompt:    prompt,
		Events:    result.Events,
		StartedAt: result.StartedAt,
		EndedAt:   result.EndedAt,
		Err:       runErr,
	}
	if err := execution.WriteEvidence(evidenceDir, snap); err != nil {
		fmt.Fprintf(os.Stderr, "warning: write evidence for plan %s: %v\n", planID, err)
	}
	capture := extractCost(result.Agent, result.Events, snap.Model, now())
	if err := execution.WriteCost(evidenceDir, capture); err != nil {
		fmt.Fprintf(os.Stderr, "warning: write cost for plan %s: %v\n", planID, err)
	}

	finalStatus := conductor.StatusCompleted
	exitReason := "completed"
	errOut := ""
	if runErr != nil {
		finalStatus = conductor.StatusFailed
		exitReason = "agent-failed"
		errOut = runErr.Error()
	}

	endState := &conductor.PlanState{
		Status:       finalStatus,
		Error:        errOut,
		Agent:        string(result.Agent),
		EvidencePath: evidenceDir,
		Attempts:     startState.Attempts,
		StartedAt:    startState.StartedAt,
		EndedAt:      now(),
		WorktreePath: ctx.WorktreeRoot,
		Branch:       ctx.Branch,
		BaseRef:      ctx.BaseRef,
		BaseHead:     ctx.BaseHead,
		InputDigest:  decision.InputDigest,
		ExitReason:   exitReason,
	}
	if finalStatus == conductor.StatusCompleted {
		endState.Merge = &conductor.MergeOutcome{
			Status:      conductor.MergePending,
			Reason:      "awaiting-merge-integration",
			AttemptedAt: now(),
		}
		if planHead, err := in.Manager.Git.Head(ctx.WorktreeRoot); err == nil {
			endState.PlanHead = planHead
		}
	}
	in.Project.State.Plans[planID] = endState
	saveErr := in.Project.SaveState()

	resultReason := decision.Reason
	if runErr != nil {
		resultReason = exitReason
	}
	out := SinglePlanResult{
		PlanID:       planID,
		Reason:       resultReason,
		Reused:       decision.Reuse,
		Context:      ctx,
		EvidencePath: evidenceDir,
		Agent:        string(result.Agent),
		Status:       finalStatus,
		Err:          runErr,
	}
	switch {
	case runErr != nil && saveErr != nil:
		out.Err = errors.Join(runErr, fmt.Errorf("save state: %w", saveErr))
		out.Reason = "agent-failed-state-save-failed"
	case runErr == nil && saveErr != nil:
		out.Err = fmt.Errorf("save state: %w", saveErr)
		out.Reason = "state-save-failed"
		out.Status = conductor.StatusFailed
	}
	switch {
	case out.Err == nil:
		progress(in.Progress, "plan %s: completed\n", planID)
	case runErr != nil && saveErr != nil:
		progress(in.Progress, "plan %s: failed — agent: %s; state save also failed: %v\n", planID, runErr.Error(), saveErr)
	case runErr != nil:
		progress(in.Progress, "plan %s: failed — %s\n", planID, runErr.Error())
	default:
		progress(in.Progress, "plan %s: state save failed — %v (agent succeeded but on-disk state may be stale)\n", planID, saveErr)
	}
	return out
}

// truncateForError reduces s to at most max runes (NOT bytes), replacing the
// tail with an ellipsis when truncation happens. Used to inline a short
// review-findings excerpt into the error message without dumping multi-KB
// diagnostics into state files; the full findings stay in evidence.
//
// Rune-based truncation is load-bearing: reviewer findings routinely contain
// multi-byte UTF-8 (em-dashes, smart quotes, non-ASCII identifiers, emoji).
// Byte-slicing at an arbitrary position would land mid-rune and produce
// invalid UTF-8 that gets persisted into .springfield state files and surfaced
// in `springfield status`.
func truncateForError(s string, max int) string {
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}

// iterationWorkComplete reports whether the events from a just-finished agent
// run would, after applying the target story's pass marker, leave EVERY story
// in the PRD passed AND the agent emitted <promise>COMPLETE</promise>. It is
// the runtime-callable form of the [completeHonored] check planrun does
// post-Run, hoisted to a closure so [coreruntime.Request.WorkCompleteCheck]
// can ask the question BEFORE the runtime decides whether to trip the turn
// cap circuit-breaker.
//
// The "hypothetical" pass application is load-bearing: when this runs,
// MarkPassed has not yet committed the iteration's pass to prd.json, so the
// in-memory PRD still shows the target story as not-yet-passed. Without
// pretending the matching pass marker landed, a legitimate final iteration
// (1 pending story, agent passes it, emits COMPLETE) would look "incomplete"
// and the runtime would falsely trip the cap. Off-target pass markers (the
// agent claims to have passed a different story than the iteration's target)
// are ignored exactly like the post-Run MarkPassed loop ignores them.
func iterationWorkComplete(events []exec.Event, p prd.PRD, targetStoryID string) bool {
	passedIDs, complete := ScanMarkers(events)
	if !complete {
		return false
	}
	matched := false
	for _, sid := range passedIDs {
		if sid == targetStoryID {
			matched = true
			break
		}
	}
	for _, s := range p.UserStories {
		if s.Passes {
			continue
		}
		if s.ID == targetStoryID && matched {
			continue
		}
		return false
	}
	return true
}

// workCompletedBeforeCrash reports whether a plan's work is provably finished
// even though the agent process exited non-zero — the honest "the crash was
// post-success teardown" signal (dogfood note A7). All three conditions must
// hold:
//
//   - completeHonored: COMPLETE was emitted AND every story passed this run
//     (Springfield's own honoring record, not just a marker seen on stdout).
//   - every UserStory in the PRD has Passes == true.
//   - the worktree advanced beyond its base ref (baseHead != worktreeHead),
//     proving commits actually landed rather than partial/no work.
//
// When true the runner maps the plan to StatusCompleted regardless of exit
// code; when false the normal exit-code-based status logic applies unchanged.
// Each condition is re-checked here so the helper is honest standalone: a
// caller cannot smuggle a completion past a still-pending story or an
// unchanged worktree.
func workCompletedBeforeCrash(completeHonored bool, p prd.PRD, baseHead, worktreeHead string) bool {
	if !completeHonored {
		return false
	}
	if !allStoriesPassed(p) {
		return false
	}
	// Empty heads can't prove the worktree moved; require both present and distinct.
	return baseHead != "" && worktreeHead != "" && baseHead != worktreeHead
}

// allStoriesPassed reports whether the PRD has at least one story and every
// story is marked Passes==true. A zero-story PRD returns false: an empty plan
// has no work to have completed.
func allStoriesPassed(p prd.PRD) bool {
	if len(p.UserStories) == 0 {
		return false
	}
	for _, s := range p.UserStories {
		if !s.Passes {
			return false
		}
	}
	return true
}

// terminalExitReason returns the canonical exit reason for the failed state.
// When exitReason is already a structured tag, use it; otherwise derive from err.
func terminalExitReason(tag string, err error) string {
	if tag != "" {
		return tag
	}
	if err != nil {
		return err.Error()
	}
	return "unknown"
}

// loadProjectGuidance reads all GuidanceFiles from controlRoot and returns
// their concatenated content. Missing files are silently skipped.
func loadProjectGuidance(controlRoot string) string {
	var b strings.Builder
	for _, name := range GuidanceFiles {
		path := filepath.Join(controlRoot, name)
		data, err := readCapped(path, maxGuidanceFileBytes)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			fmt.Fprintf(os.Stderr, "warning: read guidance %s: %v\n", name, err)
			continue
		}
		fmt.Fprintf(&b, "## %s\n%s\n", name, string(data))
	}
	return b.String()
}

func recordPreflightFailure(p *conductor.Project, planID, tag, msg string, now time.Time) {
	prior := p.State.Plans[planID]
	st := &conductor.PlanState{
		Status:     conductor.StatusFailed,
		Error:      msg,
		Attempts:   attemptsOf(prior),
		ExitReason: tag,
		EndedAt:    now,
	}
	if prior != nil {
		st.Agent = prior.Agent
		st.EvidencePath = prior.EvidencePath
		st.WorktreePath = prior.WorktreePath
		st.Branch = prior.Branch
		st.BaseRef = prior.BaseRef
		st.BaseHead = prior.BaseHead
		st.InputDigest = prior.InputDigest
		st.StartedAt = prior.StartedAt
	}
	p.State.Plans[planID] = st
}

func attemptsOf(s *conductor.PlanState) int {
	if s == nil {
		return 0
	}
	return s.Attempts
}

func agentOf(s *conductor.PlanState) string {
	if s == nil {
		return ""
	}
	return s.Agent
}

func evidenceOf(s *conductor.PlanState) string {
	if s == nil {
		return ""
	}
	return s.EvidencePath
}

func errorFromResult(result coreruntime.Result) error {
	if result.Status == coreruntime.StatusFailed {
		if result.Err != nil {
			return fmt.Errorf("agent %s failed: %w", result.Agent, result.Err)
		}
		return fmt.Errorf("agent %s exited with code %d", result.Agent, result.ExitCode)
	}
	return nil
}

func modelForAgent(id agents.ID, s agents.ExecutionSettings) string {
	switch id {
	case agents.AgentClaude:
		return s.Claude.Model
	case agents.AgentCodex:
		return s.Codex.Model
	case agents.AgentGemini:
		return s.Gemini.Model
	default:
		return ""
	}
}

func progress(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, format, args...)
}

const (
	maxPlanFileBytes     = 200 * 1024
	maxGuidanceFileBytes = 64 * 1024
)

// buildPrompt assembles the agent prompt from the plan file plus project
// guidance. Used for legacy (non-PRD) plan units.
// Reads happen against ControlRoot, never the worktree, so resume
// always sees the canonical instructions even after the worktree branch
// drifts.
func buildPrompt(controlRoot string, unit conductor.PlanUnit) (string, error) {
	planPath := filepath.Join(controlRoot, filepath.FromSlash(unit.Path))
	planBytes, err := readCapped(planPath, maxPlanFileBytes)
	if err != nil {
		return "", fmt.Errorf("read plan %s: %w", planPath, err)
	}
	var b strings.Builder
	b.WriteString("You are executing one Springfield plan in an isolated git worktree.\n")
	fmt.Fprintf(&b, "\n# Plan\n- ID: %s\n", unit.ID)
	if title := strings.TrimSpace(unit.Title); title != "" {
		fmt.Fprintf(&b, "- Title: %s\n", title)
	}
	fmt.Fprintf(&b, "- Path: %s\n", unit.Path)
	b.WriteString("\n# Plan body\n")
	b.WriteString(string(planBytes))
	if !strings.HasSuffix(string(planBytes), "\n") {
		b.WriteString("\n")
	}

	var guidance strings.Builder
	for _, name := range GuidanceFiles {
		path := filepath.Join(controlRoot, name)
		data, err := readCapped(path, maxGuidanceFileBytes)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("read guidance %s: %w", name, err)
		}
		fmt.Fprintf(&guidance, "## %s\n%s\n", name, string(data))
	}
	if guidance.Len() > 0 {
		b.WriteString("\n# Project context\n")
		b.WriteString(guidance.String())
	}

	b.WriteString("\n# Contract\n")
	b.WriteString("- Implement the plan end-to-end inside the worktree at the current working directory.\n")
	b.WriteString("- Commit when green.\n")
	b.WriteString("- Do NOT touch files under .springfield/ — that is Springfield's control plane.\n")
	b.WriteString("- Do NOT invoke springfield CLI subcommands; you are already inside a managed run.\n")
	b.WriteString("- When the plan is done, exit without asking for confirmation.\n")
	return b.String(), nil
}

func readCapped(path string, max int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > max {
		return nil, fmt.Errorf("%s exceeds %d byte cap", path, max)
	}
	return data, nil
}
