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

	"springfield/internal/core/agents"
	"springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
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

	schedule := conductor.BuildSchedule(in.Project.Config)
	next := schedule.NextPlans(in.Project.State)
	if len(next) == 0 {
		return SinglePlanResult{Reason: "no-eligible-plan"}
	}
	planID := next[0]
	if in.TargetPlanID != "" {
		// Verify the requested plan is actually eligible before overriding.
		found := false
		for _, id := range next {
			if id == in.TargetPlanID {
				found = true
				break
			}
		}
		if !found {
			return SinglePlanResult{
				PlanID: in.TargetPlanID,
				Reason: "no-eligible-plan",
			}
		}
		planID = in.TargetPlanID
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
		exitReason        = "completed"
		lastAgent         agents.ID
		iterCount         int
	)

	for iter := 1; iter <= iterCap; iter++ {
		story, pickStatus := NextStory(currentPRD)
		if pickStatus == PickAllPassed {
			// All stories already passed on first check or after marking.
			completedNormally = true
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

		result := in.Runner.Run(context.Background(), coreruntime.Request{
			AgentIDs:          in.AgentIDs,
			Prompt:            prompt,
			WorkDir:           ctx.WorktreeRoot,
			OnEvent:           in.OnEvent,
			ExecutionSettings: in.ExecutionSettings,
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

		passedIDs, complete := ScanMarkers(result.Events)
		for _, sid := range passedIDs {
			if err := MarkPassed(prdPath, sid); err != nil {
				finalRunErr = fmt.Errorf("MarkPassed failed: %w", err)
				exitReason = fmt.Sprintf("MarkPassed failed: %v", err)
				break
			}
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

		_ = AppendProgress(progressPath, fmt.Sprintf("%s iteration %d completed (passed=%d complete=%t)",
			now().UTC().Format(time.RFC3339), iter, len(passedIDs), complete))

		if iterRunErr != nil {
			finalRunErr = iterRunErr
			exitReason = "agent-failed"
			break
		}

		if complete {
			if _, stillStatus := NextStory(currentPRD); stillStatus == PickAllPassed {
				completedNormally = true
				break
			}
			// Premature COMPLETE — log warning, continue.
			_ = AppendProgress(progressPath, fmt.Sprintf("%s WARN: COMPLETE emitted but stories remain pending; ignoring marker",
				now().UTC().Format(time.RFC3339)))
		}
	}

	// After loop: check if cap was hit without completion.
	if !completedNormally && finalRunErr == nil {
		finalRunErr = fmt.Errorf("iteration cap reached without completion marker")
		exitReason = "iteration cap reached without completion marker"
	}

	// Write summary.json once at loop exit.
	terminalStatus := "completed"
	if finalRunErr != nil {
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
	if finalRunErr != nil {
		finalStatus = conductor.StatusFailed
		exitReason = terminalExitReason(exitReason, finalRunErr)
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
	out := SinglePlanResult{
		PlanID:       planID,
		Reason:       resultReason,
		Reused:       decision.Reuse,
		Context:      ctx,
		EvidencePath: evidenceDir,
		Agent:        string(lastAgent),
		Status:       finalStatus,
		Err:          finalRunErr,
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
