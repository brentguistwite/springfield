package planrun

import (
	"context"
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
)

// AgentRunner is the runtime boundary planrun depends on. The shared
// coreruntime.Runner satisfies it directly.
type AgentRunner interface {
	Run(ctx context.Context, req coreruntime.Request) coreruntime.Result
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

// SinglePlan picks the next eligible plan unit, runs preflight, prepares the
// worktree, executes the agent with WorkDir = WorktreeRoot, and persists
// truthful state. It returns SinglePlanResult.Err when the plan failed for
// any reason; the caller is responsible for surfacing the result to the
// user. State is saved on every terminal transition so a crash mid-run still
// leaves an honest record.
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
	unit, ok := in.Project.PlanUnitByID(planID)
	if !ok {
		return SinglePlanResult{PlanID: planID, Err: fmt.Errorf("plan %q is scheduled but not registered", planID)}
	}

	progress(in.Progress, "plan %s: preflight\n", planID)

	prior := in.Project.State.Plans[planID]
	decision, err := in.Manager.Prepare(PrepareInput{
		ControlRoot:  in.ControlRoot,
		WorktreeBase: in.WorktreeBase,
		Unit:         unit,
		PriorState:   prior,
		AllStates:    in.Project.State.Plans,
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

	prompt, err := buildPrompt(in.ControlRoot, unit)
	if err != nil {
		recordPreflightFailure(in.Project, planID, "prompt-build-failed", err.Error(), now())
		_ = in.Project.SaveState()
		return SinglePlanResult{PlanID: planID, Reason: "prompt-build-failed", Context: ctx, Err: err}
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
	in.Project.State.Plans[planID] = startState
	if err := in.Project.SaveState(); err != nil {
		return SinglePlanResult{PlanID: planID, Reason: "save-state-failed", Context: ctx, Err: err}
	}

	progress(in.Progress, "plan %s: dispatching agent (workdir %s)\n", planID, ctx.WorktreeRoot)
	result := in.Runner.Run(context.Background(), coreruntime.Request{
		AgentIDs:          in.AgentIDs,
		Prompt:            prompt,
		WorkDir:           ctx.WorktreeRoot,
		OnEvent:           in.OnEvent,
		ExecutionSettings: in.ExecutionSettings,
	})

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
	// On a successful execution, mark the merge integration phase as
	// pending before persisting. If the upcoming planmerge.Integrate save
	// fails for any reason, the durable record reflects "merge not yet
	// done" rather than appearing as a fully integrated legacy-style
	// completion. PlanState.IsIntegrated() returns false for any non-
	// Succeeded merge status.
	if finalStatus == conductor.StatusCompleted {
		endState.Merge = &conductor.MergeOutcome{
			Status:      conductor.MergePending,
			AttemptedAt: now(),
		}
		// Capture plan_head from the execution worktree at the boundary
		// between execution and merge phases. The slice contract names
		// plan_head as a required ref/SHA; recording it here means the
		// durable state is honest even if the process dies before
		// planmerge.Integrate runs and re-captures it.
		if planHead, err := in.Manager.Git.Head(ctx.WorktreeRoot); err == nil {
			endState.PlanHead = planHead
		}
	}
	in.Project.State.Plans[planID] = endState
	saveErr := in.Project.SaveState()

	// Reason carries the structured tag for the terminal transition. When
	// the agent failed, surface "agent-failed" so callers and CLI output
	// reflect the post-dispatch outcome rather than the pre-dispatch
	// preflight reason ("clean-first-run" / "resume-same-inputs").
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
	// SaveState failures must never be silent: the on-disk record is the
	// only honest source of truth for the plan's state, and a swallowed
	// save error leaves "running" stuck on disk.
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
// guidance. Reads happen against ControlRoot, never the worktree, so resume
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
	if desc := strings.TrimSpace(unit.Description); desc != "" {
		fmt.Fprintf(&b, "- Description: %s\n", desc)
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
