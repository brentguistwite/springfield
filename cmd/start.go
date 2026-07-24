package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
	"springfield/internal/core/config"
	coreexec "springfield/internal/core/exec"
	"springfield/internal/core/lock"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/autobranch"
	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/batchexec"
	"springfield/internal/features/conductor/planmerge"
	"springfield/internal/features/conductor/planrun"
	"springfield/internal/features/cost"
	"springfield/internal/features/wakelock"
)

// runtimeAgentRunner is a thin adapter so cmd does not need to import the
// shared coreruntime constructor everywhere. It EMBEDS the concrete runner
// rather than hand-forwarding individual methods, so every capability the
// runner exposes — Run, and the optional AssistantText the review gate
// discovers by type assertion — is promoted automatically, including any added
// later. Hand-forwarding only Run is exactly how BUG-1's decode silently went
// missing in production once AssistantText was added (the assertion failed and
// the verdict scan fell back to raw escaped stream-json); embedding makes that
// class of omission impossible.
type runtimeAgentRunner struct{ *coreruntime.Runner }

// Compile-time guards: the production wrapper MUST satisfy the review gate's
// runner contract AND carry the optional transcript decoder, or shipped reviews
// silently regress to BUG-1. Embedding satisfies both for free; these fail the
// BUILD (not a review) if the wrapper is ever downgraded to hand-forwarding.
var (
	_ planrun.AgentRunner = runtimeAgentRunner{}
	_ interface {
		AssistantText(agents.ID, []coreexec.Event) string
	} = runtimeAgentRunner{}
)

// NewStartCommand runs the active Springfield batch from its saved progress.
func NewStartCommand() *cobra.Command {
	var dir string
	var noKeepAwake bool
	var costCap float64
	var perPlanFlag bool
	var maxParallelFlag int
	var baseFlag string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Execute the active Springfield batch for the current project from its saved progress.",
		Long:  "Execute the active Springfield batch for the current project from its saved progress.\n\nRun \"springfield plan\" first to compile a batch.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if costCap < 0 {
				return fmt.Errorf("--cost-cap must be >= 0 (got %v); use 0 to disable", costCap)
			}
			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}
			root := loaded.RootDir

			lk, err := lock.Acquire(root)
			if err != nil {
				var held *lock.ErrLockHeld
				if errors.As(err, &held) {
					if held.PID != 0 {
						return fmt.Errorf("another springfield start is already running (pid %d since %s)", held.PID, held.Since.Format(time.RFC3339))
					}
					return errors.New("another springfield start is already running (holder PID unknown — may have just exited; retry if expected)")
				}
				return fmt.Errorf("acquire springfield lock: %w", err)
			}
			defer lk.Release()

			run, hasRun, err := batch.ReadRun(root)
			if err != nil {
				return err
			}

			// Emit the vendor-billing warning before any dispatch, regardless
			// of which path follows (batch-resume, single-plan-unit, or "no
			// plan" error). Operators see the warning consistently.
			emitClaudeBillingWarning(cmd.ErrOrStderr(), root, loaded.Config.Project.AgentPriority)

			// Resume semantics for a previously cost-capped batch: the user
			// must pass --cost-cap with a value strictly greater than the
			// current spend, otherwise we reject before any dispatch. This
			// keeps the new cap a deliberate choice rather than an accidental
			// silent restart at the same threshold.
			if hasRun && run.CostCapped {
				currentSpend := 0.0
				rollup, rollupErr := cost.ComputeRollup(root, run.ActiveBatchID)
				if rollupErr != nil {
					// Fail closed: a rollup we can't compute must not silently
					// look like $0 spend, which would allow ANY positive cap
					// to pass the strictly-greater check.
					return fmt.Errorf("cannot read prior spend for cost-capped resume: %w; investigate %s/.springfield/execution/plans/ or `springfield recover`", rollupErr, root)
				}
				if rollup.SkippedFiles > 0 {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %d cost.json file(s) unreadable; prior spend may under-count current actual spend\n", rollup.SkippedFiles)
				}
				currentSpend = rollup.TotalUSD
				if costCap <= 0 {
					return fmt.Errorf("cost-capped batch requires --cost-cap to resume; current spend $%.2f; pass --cost-cap $Y where Y > current spend", currentSpend)
				}
				if costCap <= currentSpend {
					return fmt.Errorf("requested cap $%.2f not greater than current spend $%.2f; pass a higher --cost-cap to resume", costCap, currentSpend)
				}
				run.CostCapped = false
				if writeErr := batch.WriteRun(root, run); writeErr != nil {
					return fmt.Errorf("clear cost-cap state for resume: %w", writeErr)
				}
			}

			if !hasRun || run.ActiveBatchID == "" {
				ran, runErr := tryRunSinglePlanUnit(cmd, root, loaded, noKeepAwake, costCap)
				if runErr != nil {
					return runErr
				}
				if ran {
					return nil
				}
				return fmt.Errorf("no Springfield plan found for this repo — run \"springfield plan\" or \"springfield plans add\" first")
			}

			paths, err := batch.NewPaths(root, run.ActiveBatchID)
			if err != nil {
				return fmt.Errorf("resolve batch paths: %w", err)
			}

			b, err := batch.ReadBatch(paths)
			if err != nil {
				if batch.IsMissingBatchError(err) {
					if recoverErr := batch.RecoverOrphan(root, run); recoverErr != nil {
						return fmt.Errorf("orphan cleanup: %w", recoverErr)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "No active batch: prior run %q was orphaned and has been archived.\nRun \"springfield plan\" to start fresh.\n", run.ActiveBatchID)
					return nil
				}
				return fmt.Errorf("read active batch: %w", err)
			}

			// Tee Springfield's own stderr into a persistent log so warnings
			// are visible interactively and durable for post-mortem.
			logPath, closeLog, logErr := openBatchLog(cmd, root, b.ID)
			if logErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to open log file: %v\n", logErr)
			} else {
				defer closeLog()
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Batch: %s\n", b.ID)
			fmt.Fprintf(w, "Title: %s\n", b.Title)
			// batchHasProgress: whether any of this batch's plans has already
			// run (used below to lock a pre-feature, unstamped batch to
			// consolidate). Loaded once here and reused for the start header.
			batchHasProgress := false
			if project, projectErr := conductor.LoadProjectRaw(root); projectErr == nil {
				// The start-header prints while this start process owns the
				// control-plane lock, so in-flight plans are genuinely running.
				// Pass the loaded plan PRDs (same helper cmd/status.go uses) so the
				// per-plan in-flight activity lines appear here too, not just in
				// `springfield status`.
				printProgressBlock(w, b, project.State, true, loadPlanPRDs(root, project.Config.PlanUnits))
				batchHasProgress = anyPlanStarted(b, project.State)
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "[warn] could not load project state: %v; progress rollup will be limited.\n", projectErr)
			}
			if logPath != "" {
				fmt.Fprintf(w, "Log: %s\n", logPath)
			}

			git := planrun.CLIGit{}

			// Resolve branch-output mode + base BEFORE auto-branch: per-plan
			// mode suppresses auto-branching (nothing merges into base, so the
			// protected-base guard has nothing to wrap), and the mode/base must
			// be durable before the first plan runs so a resume re-derives them
			// from run.json instead of re-reading the flag/config.
			modeDecision, modeErr := resolveBatchModeAndBase(git, root, run, perPlanFlag, baseFlag, loaded.Config, batchHasProgress)
			if modeErr != nil {
				return modeErr
			}
			perPlan := modeDecision.PerPlan
			batchBase := modeDecision.BatchBase
			if perPlan {
				fmt.Fprintf(w, "Branch mode: per-plan (base: %s)\n", batchBase)
			}
			if modeDecision.SuppressedPerPlanRequest {
				if run.BatchMode != "" {
					// Stamped consolidate resume: the mode is locked by design; the
					// stale-plan-ID hint below does not apply.
					fmt.Fprintf(cmd.ErrOrStderr(),
						"warning: --per-plan-branches ignored: batch %s was started in consolidate mode and cannot be switched mid-batch; continuing in consolidate mode.\n",
						b.ID)
				} else {
					// Unstamped but in progress: either a genuine pre-feature resume
					// or a fresh batch reusing a plan ID that still carries stale
					// state — surface the recovery path for the latter.
					fmt.Fprintf(cmd.ErrOrStderr(),
						"warning: --per-plan-branches ignored: batch %s is already in progress; continuing in consolidate mode. "+
							"If this is a fresh batch, a reused plan ID may be carrying stale state from a prior batch — run \"springfield batch abort\" then recompile.\n",
						b.ID)
				}
			}

			hadPriorAutoBranch := run.AutoBranchName != ""
			activation, abErr := autobranch.Activate(autobranch.Input{
				Git:                 git,
				Dir:                 root,
				BatchID:             b.ID,
				Pattern:             loaded.Config.AutoBranchPatternOrDefault(),
				Enabled:             loaded.Config.AutoBranchEnabled() && !perPlan,
				AlreadyAutoBranch:   hadPriorAutoBranch,
				PriorOriginalBranch: run.OriginalBranch,
				PriorAutoBranchName: run.AutoBranchName,
				BeforePersistCreate: func(originalBranch, branchName string) error {
					run.OriginalBranch = originalBranch
					run.AutoBranchName = branchName
					return batch.WriteRun(root, run)
				},
			}, w)
			if abErr != nil {
				// Fresh-create path may have written run state via the hook
				// before the switch failed. Roll back so the next start
				// doesn't try to resume a branch that was never created.
				if !hadPriorAutoBranch && run.AutoBranchName != "" {
					run.OriginalBranch = ""
					run.AutoBranchName = ""
					if werr := batch.WriteRun(root, run); werr != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "warning: roll back auto-branch state: %v\n", werr)
					}
				}
				return fmt.Errorf("auto-branch: %w", abErr)
			}

			// When auto-branching is active, the auto-branch IS the batch base:
			// thread it explicitly so consolidate-mode slices base off it and
			// merges publish to its ref via update-ref. This is what keeps the
			// main worktree on the operator's original branch — without it the
			// base would fall back to the current branch, forcing an in-place
			// switch. No-op in per-plan mode (activation is nil there).
			batchBase = autobranch.BaseForBatch(batchBase, activation)

			// Stamp the branch mode + base ONCE, AFTER auto-branch's clean-tree
			// check has passed (a pre-Activate run.json write would read as a dirty
			// working tree in a repo that tracks .springfield/). On resume
			// run.BatchMode is already set, so Stamp is false and the authoritative
			// on-disk values are left untouched.
			if modeDecision.Stamp {
				run.BatchMode = modeDecision.Mode
				run.BatchBase = modeDecision.BatchBase
				if writeErr := batch.WriteRun(root, run); writeErr != nil {
					return fmt.Errorf("persist batch branch mode: %w", writeErr)
				}
			}

			// Defer Restore so a panic in runBatch / archive / clear still
			// prints the auto-branch close-out (push/PR/inspect hint). Restore
			// performs no git ops — the main worktree never left the operator's
			// branch — so this is purely the closing message. The outcome
			// closure is updated below before each known exit so the message
			// matches the actual result.
			autoBranchOutcome := autobranch.OutcomeFailed
			defer func() {
				autobranch.Restore(activation, autoBranchOutcome, w)
			}()

			if !noKeepAwake && loaded.Config.KeepAwakeEnabled() {
				releaseWakelock, wlErr := wakelock.Acquire()
				if wlErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: sleep prevention unavailable: %v\n", wlErr)
				} else {
					fmt.Fprintf(w, "Sleep prevention: active\n")
					defer releaseWakelock()
				}
			}

			maxParallel := resolveMaxParallel(maxParallelFlag, loaded.Config.MaxParallel())
			result, execErr := runBatch(root, &run, b, w, logPath, costCap, perPlan, batchBase, maxParallel)

			// Interrupted by signal: leave batch state intact so the user can
			// rerun "springfield start" to resume. Do NOT archive as completed.
			if errors.Is(execErr, context.Canceled) {
				autoBranchOutcome = autobranch.OutcomeInterrupted
				fmt.Fprintf(w, "Status: interrupted\n")
				fmt.Fprintf(w, "Info: rerun \"springfield start\" to resume\n")
				return fmt.Errorf("batch %s interrupted; rerun \"springfield start\" to resume", b.ID)
			}

			// Cost-capped: persist the CostCapped state, surface the spend +
			// resume hint, and exit non-zero so CI / scripts can detect.
			// Do NOT archive — the batch is paused, not done.
			if result.CostCapped {
				run.CostCapped = true
				run.LastCheckpoint = time.Now().UTC()
				if writeErr := batch.WriteRun(root, run); writeErr != nil {
					return fmt.Errorf("persist cost-cap state: %w", writeErr)
				}
				autoBranchOutcome = autobranch.OutcomeInterrupted
				fmt.Fprintf(w, "Status: cost-capped\n")
				fmt.Fprintf(w, "Est. API cost: $%.2f (cap: $%.2f)\n", result.SpendUSD, costCap)
				// A sibling plan can fail while a parallel phase drains after
				// the cap fires: the pause wins control flow (resume works),
				// but the failure must not be swallowed — the failed plan's
				// state is persisted for "springfield recover".
				if result.Error != "" {
					fmt.Fprintf(w, "Error: %s\n", result.Error)
				}
				fmt.Fprintf(w, "Info: rerun with --cost-cap $Y to continue (Y > current spend) or remove claude from agent_priority to reduce spend\n")
				return fmt.Errorf("batch %s halted by --cost-cap at $%.2f", b.ID, result.SpendUSD)
			}

			run.LastCheckpoint = time.Now().UTC()
			if result.Error != "" {
				if !result.RunStateCleared {
					run.FatalError = result.Error
					if writeErr := batch.WriteRun(root, run); writeErr != nil {
						fmt.Fprintf(w, "Status: failed\n")
						fmt.Fprintf(w, "Error: %s\n", result.Error)
						return fmt.Errorf("batch %s failed; additionally failed to persist run state: %w", b.ID, writeErr)
					}
				}
				fmt.Fprintf(w, "Status: failed\n")
				fmt.Fprintf(w, "Error: %s\n", result.Error)
				if execErr != nil {
					return execErr
				}
				return fmt.Errorf("batch %s failed", b.ID)
			}

			// Re-read batch from disk so archive reflects slice statuses
			// written by runBatch (the caller's b is passed by value and stale).
			if fresh, readErr := batch.ReadBatch(paths); readErr == nil {
				b = fresh
			}

			// Atomic archive write is durable before we clear the cursor, so
			// archive first. If the process dies between archive-rename and
			// ClearRun, the next start sees run.json pointing at an already-
			// archived id and RecoverOrphan handles it idempotently.
			//
			// Compute the cost rollup before archiving so historical estimates
			// survive past the point where per-iter cost.json files are reaped
			// along with the live plan dir. Walking evidence dirs is best-effort
			// — a read error here must not block archive write.
			var archiveRollup *cost.Rollup
			if r, rollupErr := cost.ComputeRollup(root, b.ID); rollupErr == nil {
				archiveRollup = &r
			}
			// FinalizeBatch (completion path only) preserves the per-ticket
			// trail: it relocates evidence into the archive namespace, writes
			// an enriched entry, deregisters the batch's own plan units, and
			// clears the run cursor. The rollup MUST be computed first (above)
			// — FinalizeBatch relocates evidence, after which ComputeRollup
			// would read $0. A project-load failure falls back to the reaping
			// archive path so the batch still archives + clears.
			if project, projErr := conductor.LoadProject(root); projErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: load project for finalize: %v; archiving without per-plan enrichment\n", projErr)
				if archiveErr := batch.ArchiveBatchNormalizedWithMode(root, b, "completed", archiveRollup, run.BatchMode); archiveErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: archive completed batch %q: %v\n", b.ID, archiveErr)
				}
				if clearErr := batch.ClearRun(root); clearErr != nil {
					return fmt.Errorf("clear run state after completion: %w", clearErr)
				}
			} else if finErr := batch.FinalizeBatch(root, b, project, archiveRollup, run.BatchMode, cmd.ErrOrStderr()); finErr != nil {
				return fmt.Errorf("finalize completed batch %q: %w", b.ID, finErr)
			}

			autoBranchOutcome = autobranch.OutcomeSuccess
			fmt.Fprintf(w, "Status: completed\n")
			if archiveRollup != nil && archiveRollup.Iterations > 0 {
				fmt.Fprintln(w, formatTotalSpendLine(*archiveRollup))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	cmd.Flags().BoolVar(&noKeepAwake, "no-keep-awake", false, "disable sleep prevention for this run")
	cmd.Flags().Float64Var(&costCap, "cost-cap", 0, "Abort the batch when total spend reaches this many USD (0 = no cap). Cap-aborted batches are resumable: rerun with a higher --cost-cap to continue.")
	cmd.Flags().BoolVar(&perPlanFlag, "per-plan-branches", false, "Leave one standalone springfield/<plan> branch per plan (one PR per ticket) instead of consolidating onto a shared base. Overrides [project] branch_mode for this run. Mode is fixed at first start and is NOT flippable on resume.")
	cmd.Flags().IntVar(&maxParallelFlag, "max-parallel", 0, "Cap on concurrently running plans within a parallel-mode phase (per-plan-branches mode only; consolidate batches always run sequentially). Overrides [project] max_parallel for this run; 1 (or any lower non-zero value) disables concurrency; 0/omitted uses the configured value (default 3).")
	cmd.Flags().StringVar(&baseFlag, "base", "", "Base branch each per-plan branch is cut from (per-plan mode only). Precedence: --base > [project] base_branch > current branch. A re-passed --base on resume re-bases only not-yet-run plans.")
	return cmd
}

// batchModeDecision is the resolved branch-output mode + base for one start
// invocation, plus whether the run cursor must be stamped (fresh runs only).
type batchModeDecision struct {
	// PerPlan is the effective mode for this invocation.
	PerPlan bool
	// BatchBase is the resolved batch-wide base ref threaded to runBatch.
	// Empty in consolidate mode (base resolves per-plan, post-autobranch).
	BatchBase string
	// Stamp is true on a fresh run: the caller must set run.BatchMode/BatchBase
	// from this decision and persist once. False on resume — the on-disk values
	// are authoritative and must never be rewritten.
	Stamp bool
	// Mode is the value to stamp into run.BatchMode (only when Stamp).
	Mode string
	// SuppressedPerPlanRequest is true when the operator asked for per-plan mode
	// (via --per-plan-branches or [project] branch_mode) but it was overridden to
	// consolidate because the batch is unstamped-but-already-in-progress. The
	// caller warns on it so the dropped request is visible rather than silent —
	// in both the genuine cross-version resume and the (rarer) stale-PlanState
	// false positive where a fresh batch reuses a plan ID still carrying a
	// non-pending state from a prior --replace'd or crashed batch.
	SuppressedPerPlanRequest bool
}

// resolveBatchModeAndBase decides the effective per-plan mode and batch base
// for a start invocation, honoring the fresh-vs-resume precedence the plan
// fixes:
//
//   - Mode: fresh = --per-plan-branches flag OR [project] branch_mode; resume =
//     run.BatchMode (authoritative — a re-passed flag cannot flip a stamped
//     batch, which would mix merged + retained output across the same batch).
//   - Base (per-plan only): fresh = --base > [project] base_branch > current;
//     resume = re-passed --base > run.BatchBase > [project] base_branch >
//     current. Consolidate mode resolves no base here (kept empty) so the
//     existing per-plan, post-autobranch current-branch fallback is preserved
//     and an auto-branch run is not refused for a transient detached HEAD.
//
// batchHasProgress closes the cross-version gap: a batch that began on a
// pre-feature build has no BatchMode stamp (run.BatchMode == "") yet may already
// have run plans in consolidate mode. Treating that as "fresh" would let a
// re-passed --per-plan-branches flip it mid-batch. When unstamped-but-with-
// progress, the mode is locked to consolidate (the only mode a pre-feature batch
// could have used) and stamped so future resumes are unambiguous.
//
// A detached HEAD with no flag/config base in per-plan mode is rejected (via
// ResolveBatchBase) rather than silently picking a SHA.
func resolveBatchModeAndBase(g planrun.Git, root string, run batch.Run, perPlanFlag bool, baseFlag string, cfg config.Config, batchHasProgress bool) (batchModeDecision, error) {
	stamped := run.BatchMode != ""

	requestedPerPlan := perPlanFlag || cfg.BranchMode() == config.BranchModePerPlan

	var perPlan, suppressed bool
	switch {
	case stamped:
		// Resume of a batch started on this feature: stamped mode is authoritative.
		perPlan = run.BatchMode == string(config.BranchModePerPlan)
		// A per-plan ask against a stamped consolidate batch is dropped (mode is
		// locked) — surface it, same as the unstamped-in-progress case below,
		// rather than silently ignoring the flag.
		suppressed = requestedPerPlan && !perPlan
	case batchHasProgress:
		// Unstamped but already in progress → pre-feature consolidate batch; never flip.
		perPlan = false
		// A per-plan ask that lands here is being dropped — record it so the
		// caller can warn instead of silently ignoring the flag.
		suppressed = requestedPerPlan
	default:
		// Genuinely fresh: flag beats config.
		perPlan = requestedPerPlan
	}

	d := batchModeDecision{PerPlan: perPlan, Stamp: !stamped, SuppressedPerPlanRequest: suppressed}
	if perPlan {
		configBase := cfg.BaseBranch()
		if stamped {
			configBase = firstNonEmptyStr(run.BatchBase, configBase)
		}
		base, err := planrun.ResolveBatchBase(g, root, baseFlag, configBase)
		if err != nil {
			return batchModeDecision{}, err
		}
		d.BatchBase = base
	}
	if !stamped {
		if perPlan {
			d.Mode = string(config.BranchModePerPlan)
		} else {
			d.Mode = string(config.BranchModeConsolidate)
		}
	}
	return d, nil
}

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// BatchRunResult summarizes the outcome of running a batch.
type BatchRunResult struct {
	Status string
	Error  string
	// RunStateCleared is true when runBatch has already archived+cleared the
	// run cursor on an unrecoverable path (e.g. tamper detection). The caller
	// must not re-write run.json, or the cleared cursor gets stranded again.
	RunStateCleared bool
	// CostCapped is true when the batch hit the --cost-cap threshold and was
	// paused. The caller persists run.CostCapped and surfaces the cap status
	// to the operator instead of archiving the batch.
	CostCapped bool
	// SpendUSD reports the rollup total at the moment the cap fired. Zero
	// when CostCapped is false.
	SpendUSD float64
}

func runBatch(root string, run *batch.Run, b batch.Batch, progress io.Writer, logPath string, costCap float64, perPlan bool, batchBase string, maxParallel int) (BatchRunResult, error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runBatchWithContext(ctx, root, run, b, progress, logPath, costCap, perPlan, batchBase, maxParallel)
}

// runBatchWithContext is the testable core of runBatch. ctx controls interrupt
// detection: when ctx is cancelled mid-loop (or on entry) the function returns
// context.Canceled so the caller can distinguish a user interrupt (preserve
// batch state) from normal completion (archive + clear run.json).
// run is a pointer so the cursor's dispatch/settle checkpoints (ActivePlanIDs,
// LastCheckpoint) stay visible to the caller: RunE re-writes run.json on the
// cost-cap and failure paths after this returns, and a stale by-value copy
// would silently resurrect pre-dispatch active_plan_ids there.
func runBatchWithContext(ctx context.Context, root string, run *batch.Run, b batch.Batch, progress io.Writer, logPath string, costCap float64, perPlan bool, batchBase string, maxParallel int) (BatchRunResult, error) {
	// Check for pre-cancellation before any setup so tests and real signal
	// handlers both get the same early-exit path.
	if ctx.Err() != nil {
		return BatchRunResult{}, ctx.Err()
	}

	loaded, err := config.LoadFrom(root)
	if err != nil {
		return BatchRunResult{Error: err.Error()}, err
	}
	local, err := config.LoadLocalFrom(loaded.RootDir)
	if err != nil {
		e := fmt.Errorf("load springfield.local.toml: %w", err)
		return BatchRunResult{Error: e.Error()}, e
	}
	if len(loaded.Config.Project.AgentPriority) == 0 {
		e := fmt.Errorf("project has no agents configured: agent_priority is empty")
		return BatchRunResult{Error: e.Error()}, e
	}

	registry := agents.NewRegistry(
		claude.New(exec.LookPath),
		codex.New(exec.LookPath),
		gemini.New(exec.LookPath),
	)
	agentIDs := make([]agents.ID, 0, len(loaded.Config.Project.AgentPriority))
	for _, id := range loaded.Config.Project.AgentPriority {
		if id != "" {
			agentIDs = append(agentIDs, agents.ID(id))
		}
	}

	worktreeBase := ".worktrees"

	traceHandler, closeTrace := openAgentTrace(root, b.ID)
	defer closeTrace()

	project, err := conductor.LoadProject(root)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return BatchRunResult{Error: err.Error()}, err
		}
		// Execution config is absent. If the batch references plans, this is a
		// hard error: completing vacuously would silently archive plans that were
		// never run. Require the user to re-register or recompile.
		if len(b.PlanIDs) > 0 {
			e := fmt.Errorf("batch %q references plans %v but execution config is missing or empty; run \"springfield plans add\" to register them or \"springfield plan --replace\" to recompile",
				b.ID, b.PlanIDs)
			return BatchRunResult{Error: e.Error()}, e
		}
		// No conductor project and no plans: vacuous completion is fine.
		return BatchRunResult{}, nil
	}
	// If the project loaded but its plan registry is empty while the batch has
	// plans, that is the same class of error (stale/missing config).
	if len(project.Config.PlanUnits) == 0 && len(b.PlanIDs) > 0 {
		e := fmt.Errorf("batch %q references plans %v but execution config is missing or empty; run \"springfield plans add\" to register them or \"springfield plan --replace\" to recompile",
			b.ID, b.PlanIDs)
		return BatchRunResult{Error: e.Error()}, e
	}

	// Per-plan mode merges nothing into the base, so the protected-base guard
	// has nothing to refuse — suppress it (mirrors auto-branch suppression).
	enforceProtected := !loaded.Config.Project.AllowProtectedBase && !perPlan

	runner := &batchPlanRunner{
		project:          project,
		root:             root,
		worktreeBase:     worktreeBase,
		perPlan:          perPlan,
		progress:         progress,
		progressShared:   &syncLineWriter{w: progress},
		agentIDs:         agentIDs,
		loaded:           loaded,
		local:            local,
		registry:         registry,
		traceFor:         traceHandler,
		enforceProtected: enforceProtected,
		batchBase:        batchBase,
		costCap:          costCap,
		batchID:          b.ID,
	}
	// Runtime cursor checkpoints: keep run.json's active_plan_ids truthful
	// while plans are in flight. Both callbacks fire only from the scheduler
	// goroutine, so run.json keeps exactly one writer.
	cursor := &runCursor{root: root, run: run}
	execRes, execErr := batchexec.Execute(ctx, batchexec.Input{
		Batch:  b,
		Runner: runner,
		// Concurrency is scoped to per-plan-branches mode: consolidate-mode
		// merges are ff-only onto a moving head and structurally require
		// sequential execution.
		Parallelize: perPlan,
		MaxParallel: maxParallel,
		OnDispatch:  cursor.dispatch,
		OnSettle:    cursor.settle,
		OnPhaseStart: func(index int, phase batch.Phase, parallel bool, cap int) {
			if parallel {
				fmt.Fprintf(progress, "Phase %d/%d (parallel): up to %d of %d plans concurrently\n",
					index+1, len(b.Phases), cap, len(phase.Plans))
			}
		},
	})
	switch {
	case execRes.Cancelled:
		// Interrupted by signal or context cancel. Return the context error so
		// the caller does NOT archive as completed — batch state stays intact
		// for the user to rerun springfield start to resume.
		return BatchRunResult{}, execErr
	case execErr != nil:
		// A cost-cap pause can coexist with a plan failure (cap fired, then a
		// draining sibling failed). Carry the cap signal through so the caller
		// persists the pause state and surfaces the spend alongside the error.
		return BatchRunResult{Error: execErr.Error(), CostCapped: execRes.CostCapped, SpendUSD: execRes.SpendUSD}, execErr
	case execRes.CostCapped:
		return BatchRunResult{CostCapped: true, SpendUSD: execRes.SpendUSD}, nil
	}
	return BatchRunResult{}, nil
}

// resolveMaxParallel applies --max-parallel flag precedence: any non-zero
// flag value overrides the configured [project] max_parallel, with values
// below 1 clamping to 1 (sequential) — the same semantics the config
// resolver gives max_parallel. 0 (flag omitted) falls back to the
// configured value.
func resolveMaxParallel(flagValue, configured int) int {
	if flagValue == 0 {
		return configured
	}
	if flagValue < 1 {
		return 1
	}
	return flagValue
}

// runCursor keeps run.json's active_plan_ids truthful while plans are in
// flight. Its methods are invoked only from the batchexec scheduler goroutine
// (the OnDispatch/OnSettle contract), so run.json keeps exactly one writer.
// Writes are best-effort — the durable execution record is state.json, not
// the cursor.
type runCursor struct {
	root   string
	run    *batch.Run
	active []string
}

func (c *runCursor) dispatch(planID string) {
	c.active = append(c.active, planID)
	c.checkpoint()
}

func (c *runCursor) settle(planID string) {
	for i, id := range c.active {
		if id == planID {
			c.active = append(c.active[:i], c.active[i+1:]...)
			break
		}
	}
	c.checkpoint()
}

func (c *runCursor) checkpoint() {
	c.run.ActivePlanIDs = append([]string(nil), c.active...)
	c.run.LastCheckpoint = time.Now()
	_ = batch.WriteRun(c.root, *c.run)
}

// batchPlanRunner adapts the plan execution + integration pipeline to
// batchexec.PlanRunner. It owns everything batchexec deliberately doesn't
// know about: agents, config, git integration, and progress reporting.
type batchPlanRunner struct {
	project          *conductor.Project
	root             string
	worktreeBase     string
	perPlan          bool
	progress         io.Writer
	progressShared   *syncLineWriter
	agentIDs         []agents.ID
	loaded           config.Loaded
	local            config.LocalConfig
	registry         agents.Registry
	traceFor         func(planID string) coreexec.EventHandler
	enforceProtected bool
	batchBase        string
	costCap          float64
	batchID          string
}

// IsTerminal reports a FULLY INTEGRATED plan (batchexec's terminal contract).
// StatusCompleted alone is NOT terminal — see the re-entry branch in RunPlan.
func (r *batchPlanRunner) IsTerminal(planID string) bool {
	st, ok := r.project.ReadPlan(planID)
	return ok && st.IsIntegrated()
}

// planProgress picks the progress writer for one dispatch: concurrent plans
// get a phase-stable per-plan prefix on every line through the shared
// line-atomic writer; sequential execution writes through untouched
// (byte-identical output to the pre-parallelism loop). The returned flush
// must be called when the plan settles to emit any trailing partial line.
func (r *batchPlanRunner) planProgress(planID string, info batchexec.RunInfo) (io.Writer, func()) {
	if !info.Concurrent {
		return r.progress, func() {}
	}
	pw := newPrefixWriter(r.progressShared, planID)
	return pw, pw.Flush
}

// tamperGuard builds the per-dispatch control-plane guard. Concurrent
// dispatches watch ONLY the plan's own subtree and skip run.json (empty
// controlRoot disables that check): during this plan's agent window the
// scheduler legitimately rewrites the cursor on every sibling
// dispatch/settle, and concurrently running siblings legitimately write
// their own progress.md/prd.json under the shared plans tree — a whole-tree
// byte-compare would deterministically false-trip tamper detection, and
// Restore would revert sibling state and clobber the live cursor with a
// stale snapshot. Scoping the guard per plan keeps detection meaningful
// (each plan's dir is watched by exactly the guard whose agent window it
// must stay stable in); sequential dispatches keep the historical
// whole-tree + run.json protection.
func (r *batchPlanRunner) tamperGuard(planID string, info batchexec.RunInfo) *planDirTamperGuard {
	plansRoot := filepath.Join(r.root, ".springfield", "plans")
	if !info.Concurrent {
		return &planDirTamperGuard{planDir: plansRoot, controlRoot: r.root}
	}
	planDir := filepath.Join(plansRoot, planID)
	if unit, ok := r.project.PlanUnitByID(planID); ok {
		planDir = filepath.Dir(filepath.Join(r.root, filepath.FromSlash(unit.Path)))
	}
	return &planDirTamperGuard{planDir: planDir}
}

func (r *batchPlanRunner) RunPlan(ctx context.Context, planID string, info batchexec.RunInfo) batchexec.Outcome {
	progress, flush := r.planProgress(planID, info)
	defer flush()
	var onEvent coreexec.EventHandler
	if r.traceFor != nil {
		onEvent = r.traceFor(planID)
	}

	// Re-entry: a plan that already finished execution (Status=Completed)
	// but is not yet integrated — a crash between execution and integration,
	// or a prior integration whose cleanup failed (e.g. a transient
	// `git worktree remove` failure) — must NOT be re-dispatched through
	// SinglePlan, which would reject it with preflight-already-completed and
	// deadlock the batch. Drive only the integration/cleanup step instead.
	if st, ok := r.project.ReadPlan(planID); ok && st.Status == conductor.StatusCompleted {
		fmt.Fprintf(progress, "Plan: %s\n", planID)
		fmt.Fprintf(progress, "Status: resuming integration\n")
		if halt, err := integratePlan(r.perPlan, r.project, r.root, r.worktreeBase, planID, progress); halt != nil {
			return batchexec.Outcome{Err: err}
		}
		return batchexec.Outcome{}
	}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:              r.project,
		ControlRoot:          r.root,
		ProjectRoot:          r.root,
		WorktreeBase:         r.worktreeBase,
		AgentIDs:             r.agentIDs,
		ExecutionSettings:    r.loaded.Config.ExecutionSettings(),
		ReviewConfig:         r.local.Review,
		VerifyConfig:         r.loaded.Config.Verify,
		Runner:               runtimeAgentRunner{coreruntime.NewRunner(r.registry)},
		Manager:              planrun.NewManager(),
		OnEvent:              onEvent,
		Progress:             progress,
		TargetPlanID:         planID,
		EnforceProtectedBase: r.enforceProtected,
		BatchBaseRef:         r.batchBase,
		TamperGuard:          r.tamperGuard(planID, info),
		Ctx:                  ctx,
		MaxTurnsPerIteration: r.loaded.Config.MaxTurnsPerIteration(),
		MinFreeDiskBytes:     r.loaded.Config.MinFreeDiskBytes(),
		CostCapUSD:           r.costCap,
		BatchID:              r.batchID,
	})
	if res.Reason == "no-eligible-plan" {
		// The target plan is not registered in the conductor schedule —
		// either not yet registered or already terminal. Stop the batch.
		return batchexec.Outcome{NoEligiblePlan: true}
	}
	// Cost-cap fired inside the plan's iteration loop. Surface the pause
	// to the caller WITHOUT marking the batch as failed.
	if res.CostCapped {
		fmt.Fprintf(progress, "Plan: %s\n", res.PlanID)
		fmt.Fprintf(progress, "Status: cost-capped (%s)\n", res.Reason)
		return batchexec.Outcome{CostCapped: true, SpendUSD: res.SpendUSD}
	}
	if res.Err != nil {
		fmt.Fprintf(progress, "Plan: %s\n", res.PlanID)
		if res.Status == conductor.StatusNeedsHuman {
			fmt.Fprintf(progress, "Status: needs human review (%s)\n", res.Reason)
		} else {
			fmt.Fprintf(progress, "Status: failed (%s)\n", res.Reason)
		}
		fmt.Fprintf(progress, "Error: %s\n", res.Err.Error())
		return batchexec.Outcome{Err: res.Err}
	}
	fmt.Fprintf(progress, "Plan: %s\n", res.PlanID)
	fmt.Fprintf(progress, "Status: completed\n")
	if res.EvidencePath != "" {
		fmt.Fprintf(progress, "Evidence: %s\n", res.EvidencePath)
	}

	if halt, err := integratePlan(r.perPlan, r.project, r.root, r.worktreeBase, res.PlanID, progress); halt != nil {
		return batchexec.Outcome{Err: err}
	}
	return batchexec.Outcome{}
}

// anyPlanStarted reports whether any plan in the batch has a recorded state
// past pending — i.e. the batch has already begun executing. Used to detect a
// pre-feature batch (no BatchMode stamp) that is mid-flight, so its branch mode
// cannot be flipped by a re-passed flag on resume.
//
// Limitation: PlanState has no batch provenance, so a genuinely fresh batch
// that reuses a plan ID still carrying stale state from a prior batch is read as
// in-progress and locked to consolidate. This is reachable (verified, not
// hypothetical): `springfield plan --replace` archives the prior batch via
// ArchiveBatchNormalized, which does NOT clear State.Plans, and a reused unit
// whose Title/Path/Order are unchanged is preserved (RemovePlanUnit is skipped),
// so a mid-flight non-Completed state survives; a crashed batch followed by a
// new batch reusing the slug reaches the same state. A stale Completed state is
// already rejected upstream by preflight-already-completed.
//
// Locking to consolidate is the conservative direction (no mixed-output
// corruption), and the override is no longer silent — resolveBatchModeAndBase
// sets SuppressedPerPlanRequest and the start path warns. The root fix (batch
// provenance on PlanState, or clearing State.Plans at compile/replace) lives
// outside this package and is deferred as out-of-scope here.
func anyPlanStarted(b batch.Batch, state *conductor.State) bool {
	if state == nil {
		return false
	}
	for _, id := range b.PlanIDs {
		if ps := state.Plans[id]; ps != nil && ps.Status != "" && ps.Status != conductor.StatusPending {
			return true
		}
	}
	return false
}

// integratePlan runs the post-execution integration step for one completed
// plan — Retain in per-plan mode (keep the standalone branch, drop the
// worktree) or Integrate in consolidate mode (ff-merge into the base) — and
// returns a non-nil *BatchRunResult (with the matching error) when the batch
// must halt; nil means the plan integrated cleanly and the loop may advance.
// Shared by the normal dispatch path and the completed-but-not-integrated
// re-entry path so both apply identical halt semantics.
func integratePlan(perPlan bool, project *conductor.Project, root, worktreeBase, planID string, progress io.Writer) (*BatchRunResult, error) {
	var mergeRes planmerge.IntegrateResult
	if perPlan {
		mergeRes = planmerge.Retain(planmerge.RetainInput{
			Project:     project,
			PlanID:      planID,
			ControlRoot: root,
			Progress:    progress,
		})
	} else {
		mergeRes = planmerge.Integrate(planmerge.IntegrateInput{
			Project:      project,
			PlanID:       planID,
			ControlRoot:  root,
			WorktreeBase: worktreeBase,
			Progress:     progress,
		})
	}
	renderMergeOutcome(progress, mergeRes)
	if mergeRes.Err != nil {
		e := fmt.Errorf("plan %s merge integration failed: %w", planID, mergeRes.Err)
		return &BatchRunResult{Error: e.Error()}, e
	}
	if mergeRes.Merge != nil && mergeRes.Merge.Status != conductor.MergeSucceeded {
		e := fmt.Errorf("plan %s merge %s: %s", planID, mergeRes.Merge.Status, mergeRes.Merge.Reason)
		return &BatchRunResult{Error: e.Error()}, e
	}
	if mergeRes.Cleanup != nil && mergeRes.Cleanup.Status == conductor.CleanupFailed {
		e := fmt.Errorf("plan %s merge succeeded but cleanup failed: artifacts preserved", planID)
		return &BatchRunResult{Error: e.Error()}, e
	}
	if mergeRes.Merge != nil && mergeRes.Merge.SourceSyncStatus == "failed" {
		e := fmt.Errorf("plan %s merge succeeded but source resync failed: %s", planID, mergeRes.Merge.SourceSyncError)
		return &BatchRunResult{Error: e.Error()}, e
	}
	return nil, nil
}

// controlPlaneSnapshot captures every Springfield-owned file under
// .springfield/plans/<id>/ plus run.json, taken between Springfield's
// pre-agent write and the agent's execution. Springfield does not touch any
// of these files while the agent is running, so any post-agent byte-level
// difference is tamper.
//
// tree keys are plan-dir-relative paths using forward slashes (stable across
// platforms). Bytes are stored in full so the pre-agent state can be restored
// wholesale on tamper without a separate read pass.
type controlPlaneSnapshot struct {
	tree     map[string][]byte
	runBytes []byte
}

// Snapshot byte caps: generous enough to never reject a legitimate plan
// (2 MiB source.md is well below the per-file cap; realistic plan trees
// stay under a few MiB total), tight enough to catch pathological bloat
// before the in-memory snapshot OOMs the CLI.
const (
	snapshotFileMaxBytes = 10 * 1024 * 1024  // 10 MiB per file
	snapshotTreeMaxBytes = 100 * 1024 * 1024 // 100 MiB cumulative
)

func snapshotControlPlane(root string, paths batch.Paths) (controlPlaneSnapshot, error) {
	tree, err := snapshotPlanTree(paths.PlanDir())
	if err != nil {
		return controlPlaneSnapshot{}, fmt.Errorf("snapshot plan dir: %w", err)
	}
	runBytes, err := os.ReadFile(batch.RunPath(root))
	if err != nil {
		return controlPlaneSnapshot{}, fmt.Errorf("read run.json: %w", err)
	}
	return controlPlaneSnapshot{tree: tree, runBytes: runBytes}, nil
}

// snapshotPlanTree walks planDir and returns a relpath->bytes map. Missing
// planDir is an error (the caller has just written batch.json into it).
//
// Non-regular entries (symlinks, devices, fifos, sockets) are rejected:
// Springfield only writes regular files under the plan dir, so any other
// node is an integrity violation. Reads use O_NOFOLLOW as defense-in-depth
// against a symlink being swapped in after the d.Type() check.
//
// No basename is excluded: tmp scratch files from writeFileAtomic are always
// renamed out before snapshot runs, so any ".tmp-*" still present at snapshot
// or compare time is an agent artifact and must be treated like any other
// file — captured by snapshot (so byte changes are caught) or flagged as
// "added" by comparison.
func snapshotPlanTree(planDir string) (map[string][]byte, error) {
	out := make(map[string][]byte)
	var totalBytes int64
	err := filepath.WalkDir(planDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(planDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&(fs.ModeSymlink|fs.ModeDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeIrregular) != 0 {
			return fmt.Errorf("non-regular entry %q", rel)
		}

		f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return fmt.Errorf("open %s: %w", rel, err)
		}
		// Read at most cap+1 bytes so we can detect overflow without
		// slurping an arbitrarily large file into memory.
		data, err := io.ReadAll(io.LimitReader(f, snapshotFileMaxBytes+1))
		closeErr := f.Close()
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		if closeErr != nil {
			return fmt.Errorf("close %s: %w", rel, closeErr)
		}
		if len(data) > snapshotFileMaxBytes {
			return fmt.Errorf("%s exceeds per-file cap", rel)
		}
		totalBytes += int64(len(data))
		if totalBytes > snapshotTreeMaxBytes {
			return fmt.Errorf("plan tree exceeds total cap")
		}
		out[rel] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// tamperForensicsContext carries the run-local context used to populate a
// forensics sidecar when tamper is detected.
type tamperForensicsContext struct {
	batchID      string
	sliceID      string
	agentID      string
	agentLogPath string
	exitCode     int
}

// detectAndRecoverTamper enforces the Workstream-B invariant that agents must
// not modify Springfield control-plane state. On any byte-level difference,
// the snapshot is restored, the batch is archived as "state-tampered", and
// run.json is cleared. A forensics sidecar is written into the archive dir
// regardless of whether the archive write itself was a no-op (e.g. already
// archived from a prior call under the same reason).
func detectAndRecoverTamper(root string, paths batch.Paths, snap controlPlaneSnapshot, forensics tamperForensicsContext) error {
	reason := compareControlPlane(root, paths, snap, allowedEvidenceRelPaths(forensics.sliceID))
	if reason == "" {
		return nil
	}

	// Capture post-tamper bytes before restore overwrites them.
	postBytes := capturePostBytesForReason(root, paths, reason)

	var restoreErr error
	if err := restoreControlPlane(root, paths, snap); err != nil {
		restoreErr = err
	}

	// Forensics sidecar captures the what/where/why of the tamper.
	// Best-effort: a missing sidecar must never escalate past the tamper
	// message that already tells the operator what happened.
	preBytes := preBytesForReason(snap, reason)
	_ = writeTamperSidecar(root, forensics, reason, preBytes, postBytes)
	// Raw byte blobs for diff: caller can `diff <pre> <post>` to see the
	// exact mutation. Best-effort, unconditional on tamper.
	_ = writeTamperBlobs(root, forensics, preBytes, postBytes)

	// Note: we intentionally do NOT archive the batch on tamper. The snapshot
	// has been restored; the batch is coherent again. The current slice is
	// marked failed by the caller, but the batch itself stays active so the
	// user can retry without recompiling all slices. The forensics sidecar
	// records what happened for post-mortem.

	msg := fmt.Sprintf("state tampered by agent (%s)", reason)
	if restoreErr != nil {
		msg += fmt.Sprintf("; restore failed: %v", restoreErr)
	}
	return fmt.Errorf("%s", msg)
}

// capturePostBytesForReason reads the diverged file's current bytes (agent's
// mutation) so they can be recorded in the forensics sidecar before restore
// overwrites them. Returns nil when the divergence is the cursor file
// itself or when the file no longer exists (agent deleted it).
func capturePostBytesForReason(root string, paths batch.Paths, reason string) []byte {
	rel, kind := parseReason(reason)
	switch kind {
	case reasonPlanFileChanged, reasonPlanFileAdded:
		data, err := os.ReadFile(filepath.Join(paths.PlanDir(), filepath.FromSlash(rel)))
		if err != nil {
			return nil
		}
		return data
	case reasonRunChanged:
		data, err := os.ReadFile(batch.RunPath(root))
		if err != nil {
			return nil
		}
		return data
	default:
		return nil
	}
}

// preBytesForReason extracts the pre-agent snapshot bytes matching the
// divergence reason, or nil when the divergence was an added-by-agent file.
func preBytesForReason(snap controlPlaneSnapshot, reason string) []byte {
	rel, kind := parseReason(reason)
	switch kind {
	case reasonPlanFileChanged, reasonPlanFileMissing:
		return snap.tree[rel]
	case reasonRunChanged, reasonRunMissing:
		return snap.runBytes
	default:
		return nil
	}
}

type reasonKind int

const (
	reasonUnknown reasonKind = iota
	reasonPlanFileChanged
	reasonPlanFileAdded
	reasonPlanFileMissing
	reasonRunChanged
	reasonRunMissing
)

// parseReason splits the compareControlPlane reason string back into a
// relpath + kind. Reasons are structured ("<rel> changed|added|missing" or
// "run.json changed|missing") so this is a shallow parser, not a regex.
func parseReason(reason string) (string, reasonKind) {
	switch {
	case strings.HasSuffix(reason, " changed"):
		rel := strings.TrimSuffix(reason, " changed")
		if rel == "run.json" {
			return rel, reasonRunChanged
		}
		return rel, reasonPlanFileChanged
	case strings.HasSuffix(reason, " added"):
		return strings.TrimSuffix(reason, " added"), reasonPlanFileAdded
	case strings.HasSuffix(reason, " missing"):
		rel := strings.TrimSuffix(reason, " missing")
		if rel == "run.json" {
			return rel, reasonRunMissing
		}
		return rel, reasonPlanFileMissing
	}
	return reason, reasonUnknown
}

// writeTamperSidecar persists a best-effort forensic record to the archive
// dir. Filename embeds unix-nano so concurrent events never collide.
func writeTamperSidecar(root string, ctx tamperForensicsContext, reason string, pre, post []byte) error {
	archiveDir := batch.ArchiveDir(root)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return err
	}
	sidecar := map[string]any{
		"batch_id":       ctx.batchID,
		"slice_id":       ctx.sliceID,
		"reason":         reason,
		"pre_sha256":     sha256Hex(pre),
		"post_sha256":    sha256Hex(post),
		"agent_id":       ctx.agentID,
		"agent_log_path": ctx.agentLogPath,
		"exit_code":      ctx.exitCode,
		"detected_at":    time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(sidecar, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	name := fmt.Sprintf("%s.%d.tamper.json", ctx.batchID, time.Now().UTC().UnixNano())
	path := filepath.Join(archiveDir, name)

	tmp, err := os.CreateTemp(archiveDir, ".tmp-"+name+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func sha256Hex(data []byte) string {
	if data == nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// restoreControlPlane rewrites the plan dir tree and run.json back to the
// pre-agent snapshot. Files absent from the snapshot but present on disk
// (agent-created) are removed; files absent from disk but present in the
// snapshot (agent-deleted) are recreated.
//
// Writes go through writeFileReplacingNonRegular so a symlink, device, or
// other non-regular node planted by the agent is unlinked before the new
// bytes are written. The restore NEVER follows a link out of the control
// plane.
func restoreControlPlane(root string, paths batch.Paths, snap controlPlaneSnapshot) error {
	planDir := paths.PlanDir()
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		return fmt.Errorf("recreate plan dir: %w", err)
	}

	// Enumerate with Lstat so we see symlinks/devices the agent may have
	// planted — snapshotPlanTree may reject them outright after F2, but the
	// restore pass still needs to remove stray nodes before rewriting.
	onDisk, err := enumeratePlanTreeRaw(planDir)
	if err != nil {
		return fmt.Errorf("enumerate plan dir: %w", err)
	}
	for rel := range onDisk {
		if _, keep := snap.tree[rel]; !keep {
			abs := filepath.Join(planDir, filepath.FromSlash(rel))
			if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove stray %s: %w", rel, err)
			}
		}
	}
	for rel, data := range snap.tree {
		abs := filepath.Join(planDir, filepath.FromSlash(rel))
		if err := writeFileReplacingNonRegular(abs, data, 0o644); err != nil {
			return fmt.Errorf("restore %s: %w", rel, err)
		}
	}
	if err := writeFileReplacingNonRegular(batch.RunPath(root), snap.runBytes, 0o644); err != nil {
		return fmt.Errorf("restore run.json: %w", err)
	}
	return nil
}

// enumeratePlanTreeRaw lists every file under planDir (relpath keys, forward
// slashes) without reading bytes and without rejecting non-regular entries.
// Used by restoreControlPlane to find stray nodes — including non-regular
// ones planted by the agent — so they can be unlinked before restore
// rewrites. No basename is excluded: any ".tmp-*" entry visible at restore
// time is an agent artifact and must be cleaned up.
func enumeratePlanTreeRaw(planDir string) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	err := filepath.WalkDir(planDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(planDir, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// writeFileReplacingNonRegular writes data to abs atomically. If abs is an
// existing symlink/device/fifo/socket, the node is removed first so the write
// never follows the link. Uses sibling tmp + fsync + rename, with a chmod to
// the caller's requested mode (os.CreateTemp starts at 0600).
func writeFileReplacingNonRegular(abs string, data []byte, mode os.FileMode) error {
	if info, err := os.Lstat(abs); err == nil {
		if !info.Mode().IsRegular() {
			if rmErr := os.Remove(abs); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
				return fmt.Errorf("remove non-regular node: %w", rmErr)
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("lstat: %w", err)
	}

	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".tmp-restore-*")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		cleanup()
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := os.Rename(tmpName, abs); err != nil {
		cleanup()
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// compareControlPlane returns "" when on-disk state matches the snapshot
// byte-for-byte; otherwise a plan-dir-relative path naming the first file
// that diverged (or "run.json" / "run.json missing" for the shared cursor).
// Divergence is ordered: added/missing/changed files under the plan dir
// first (stable alpha order), then run.json.
func compareControlPlane(root string, paths batch.Paths, snap controlPlaneSnapshot, allowed map[string]bool) string {
	current, err := snapshotPlanTree(paths.PlanDir())
	if err != nil {
		return fmt.Sprintf("plan dir unreadable: %v", err)
	}
	if reason := firstTreeDivergence(snap.tree, current, allowed); reason != "" {
		return reason
	}
	runNow, err := os.ReadFile(batch.RunPath(root))
	if err != nil {
		return fmt.Sprintf("run.json missing: %v", err)
	}
	if !bytes.Equal(runNow, snap.runBytes) {
		return "run.json changed"
	}
	return ""
}

// firstTreeDivergence compares two relpath->bytes maps and returns a reason
// string identifying the first divergent relpath, or "" when they match.
// Iteration is sorted so the reason is deterministic across runs.
func firstTreeDivergence(want, got map[string][]byte, allowed map[string]bool) string {
	keys := make([]string, 0, len(want)+len(got))
	seen := make(map[string]bool, len(want)+len(got))
	for k := range want {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for k := range got {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, rel := range keys {
		if allowed[rel] {
			continue
		}
		w, okWant := want[rel]
		g, okGot := got[rel]
		switch {
		case okWant && !okGot:
			return rel + " missing"
		case !okWant && okGot:
			return rel + " added"
		case !bytes.Equal(w, g):
			return rel + " changed"
		}
	}
	return ""
}

func allowedEvidenceRelPaths(sliceID string) map[string]bool {
	if sliceID == "" {
		return nil
	}
	base := filepath.ToSlash(filepath.Join("evidence", sliceID))
	return map[string]bool{
		filepath.ToSlash(filepath.Join(base, "meta.json")):          true,
		filepath.ToSlash(filepath.Join(base, "events.jsonl")):       true,
		filepath.ToSlash(filepath.Join(base, "assistant_text.txt")): true,
		filepath.ToSlash(filepath.Join(base, "prompt.txt")):         true,
	}
}

// openBatchLog tees Springfield's cobra stdout+stderr into a persistent log
// under .springfield/logs/. The terminal still receives both streams.
func openBatchLog(cmd *cobra.Command, root, batchID string) (string, func(), error) {
	logsDir := filepath.Join(root, ".springfield", "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return "", nil, err
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	logPath := filepath.Join(logsDir, fmt.Sprintf("%s-%s.log", batchID, ts))
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", nil, err
	}
	cmd.SetOut(io.MultiWriter(cmd.OutOrStdout(), f))
	cmd.SetErr(io.MultiWriter(cmd.ErrOrStderr(), f))
	closer := func() { _ = f.Close() }
	return logPath, closer, nil
}

// writeTamperBlobs writes the pre-agent snapshot bytes and post-agent
// bytes of the diverged file to the archive dir, so operators can run
// `diff` between them to see exactly what changed. Filenames share the
// sidecar's unix-nano prefix when possible (but collisions are fine —
// we add our own timestamp). Best-effort forensic.
func writeTamperBlobs(root string, ctx tamperForensicsContext, pre, post []byte) error {
	archiveDir := batch.ArchiveDir(root)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return err
	}
	ns := time.Now().UTC().UnixNano()
	base := fmt.Sprintf("%s.%d", ctx.batchID, ns)
	if pre != nil {
		_ = os.WriteFile(filepath.Join(archiveDir, base+".tamper.pre"), pre, 0o644)
	}
	if post != nil {
		_ = os.WriteFile(filepath.Join(archiveDir, base+".tamper.post"), post, 0o644)
	}
	return nil
}

// openAgentTrace opens a per-batch JSONL file that captures every exec
// event (stdout, stderr, lifecycle) from the agent. Returns a handler that
// appends events as JSON lines and a closer. On open failure returns nil
// handler (events discarded) and a noop closer — trace is best-effort
// diagnostic, not load-bearing.
func openAgentTrace(root, batchID string) (func(planID string) coreexec.EventHandler, func()) {
	logsDir := filepath.Join(root, ".springfield", "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return nil, func() {}
	}
	name := fmt.Sprintf("%s-%s.agent-trace.jsonl", batchID, time.Now().UTC().Format("20060102T150405Z"))
	path := filepath.Join(logsDir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, func() {}
	}
	closer := func() { _ = f.Close() }
	// One append mutex for the whole file: concurrently-running plans each
	// get their own handler (stamping their plan ID onto every event) but
	// share the serialized append so JSONL lines never interleave.
	var mu sync.Mutex
	handlerFor := func(planID string) coreexec.EventHandler {
		return func(e coreexec.Event) {
			data, err := json.Marshal(map[string]any{
				"type": string(e.Type),
				"time": e.Time.UTC().Format(time.RFC3339Nano),
				"data": e.Data,
				"plan": planID,
			})
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			_, _ = f.Write(append(data, '\n'))
		}
	}
	return handlerFor, closer
}

// tryRunSinglePlanUnit handles the parity-2 single-plan worktree flow when no
// active batch is present. Returns (true, nil) when a plan ran (success or
// failure with state persisted); (false, nil) when no plan-unit registry is
// configured so the caller can fall through to its legacy "no batch" error;
// (false, err) when something prevented even attempting plan execution.
func tryRunSinglePlanUnit(cmd *cobra.Command, root string, loaded config.Loaded, noKeepAwake bool, costCap float64) (bool, error) {
	project, err := conductor.LoadProject(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if project.Config == nil || len(project.Config.PlanUnits) == 0 {
		return false, nil
	}
	if changed := project.NormalizeStaleRunning(time.Now()); len(changed) > 0 {
		if err := project.SaveState(); err != nil {
			return true, err
		}
	}

	w := cmd.OutOrStdout()

	logsDir := filepath.Join(root, ".springfield", "logs")
	_ = os.MkdirAll(logsDir, 0o755)
	logPath := filepath.Join(logsDir, fmt.Sprintf("plan-run-%s.log", time.Now().UTC().Format("20060102T150405Z")))
	if f, lerr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); lerr == nil {
		cmd.SetOut(io.MultiWriter(cmd.OutOrStdout(), f))
		cmd.SetErr(io.MultiWriter(cmd.ErrOrStderr(), f))
		defer f.Close()
		fmt.Fprintf(cmd.OutOrStdout(), "Log: %s\n", logPath)
	}

	if !noKeepAwake && loaded.Config.KeepAwakeEnabled() {
		releaseWakelock, wlErr := wakelock.Acquire()
		if wlErr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: sleep prevention unavailable: %v\n", wlErr)
		} else {
			fmt.Fprintf(w, "Sleep prevention: active\n")
			defer releaseWakelock()
		}
	}

	registry := agents.NewRegistry(
		claude.New(exec.LookPath),
		codex.New(exec.LookPath),
		gemini.New(exec.LookPath),
	)
	if len(loaded.Config.Project.AgentPriority) == 0 {
		return false, fmt.Errorf("project has no agents configured: agent_priority is empty. Run \"springfield init\" to select agents.")
	}
	agentIDs := make([]agents.ID, 0, len(loaded.Config.Project.AgentPriority))
	for _, id := range loaded.Config.Project.AgentPriority {
		if id == "" {
			continue
		}
		agentIDs = append(agentIDs, agents.ID(id))
	}

	worktreeBase := project.Config.WorktreeBase
	if worktreeBase == "" {
		worktreeBase = ".worktrees"
	}

	// Load springfield.local.toml ONCE per CLI invocation. Loading inside the
	// per-plan loop would let a mid-batch edit silently take effect on the
	// next plan, diverging from the batch-path semantics where review config
	// is stable for the whole batch.
	local, err := config.LoadLocalFrom(loaded.RootDir)
	if err != nil {
		return true, fmt.Errorf("load springfield.local.toml: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	schedule := conductor.BuildSchedule(project.Config)

	if project.State.Queue == nil {
		project.State.Queue = &conductor.QueueState{}
	}
	project.State.Queue.Status = conductor.QueueRunning
	if project.State.Queue.StartedAt.IsZero() {
		project.State.Queue.StartedAt = time.Now()
	}
	project.State.Queue.EndedAt = time.Time{}
	project.State.Queue.StopReason = ""
	if err := project.SaveState(); err != nil {
		return true, err
	}

	saveQueueState := func() {
		if err := project.SaveState(); err != nil {
			fmt.Fprintf(w, "warning: failed to persist queue state: %v\n", err)
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			project.State.Queue.Status = conductor.QueueStopped
			project.State.Queue.StopReason = "interrupted by signal"
			project.State.Queue.EndedAt = time.Now()
			saveQueueState()
			fmt.Fprintln(w, "Queue stopped: interrupted by signal")
			return true, nil
		}

		next := schedule.NextPlans(project.State)
		if len(next) == 0 {
			break
		}

		project.State.Queue.ActivePlanID = next[0]
		project.State.Queue.Heartbeat = time.Now()
		saveQueueState()

		planErr := runOnePlan(ctx, w, project, root, worktreeBase, agentIDs, loaded, local, registry, costCap)
		if planErr != nil {
			project.State.Queue.Status = conductor.QueueHalted
			project.State.Queue.StopReason = planErr.Error()
			project.State.Queue.EndedAt = time.Now()
			project.State.Queue.ActivePlanID = ""
			saveQueueState()
			return true, planErr
		}
	}

	project.State.Queue.Status = conductor.QueueCompleted
	project.State.Queue.ActivePlanID = ""
	project.State.Queue.EndedAt = time.Now()
	saveQueueState()

	completed, total := schedule.Progress(project.State)
	fmt.Fprintf(w, "Queue completed: %d of %d plans integrated\n", completed, total)
	return true, nil
}

// runOnePlan executes or merge-integrates the next eligible plan. Returns nil
// on success, error on failure/merge-refused/cleanup-failed. local is passed
// in from the caller so the springfield.local.toml load is stable for the
// whole single-plan batch (loading per-call would let mid-batch edits
// silently change review behavior).
func runOnePlan(ctx context.Context, w io.Writer, project *conductor.Project, root, worktreeBase string, agentIDs []agents.ID, loaded config.Loaded, local config.LocalConfig, registry agents.Registry, costCap float64) error {
	enforceProtected := !loaded.Config.Project.AllowProtectedBase

	if planID, ok := nextNonIntegratedCompletedPlan(project); ok {
		// The fresh-execution path is gated by planrun.Prepare, but a
		// previously-completed plan that has not yet integrated reaches
		// planmerge.Integrate without going back through Prepare. Re-apply
		// the guard here against the recorded BaseRef so a resume after
		// crash, target-drift refusal, or post-completion config flip cannot
		// silently advance a protected branch past origin.
		if enforceProtected {
			if prior := project.State.Plans[planID]; prior != nil && planrun.IsProtectedBase(prior.BaseRef) {
				fmt.Fprintf(w, "Plan: %s\n", planID)
				fmt.Fprintf(w, "Status: failed (preflight-protected-base)\n")
				// Recovery path: the recorded BaseRef cannot be rewritten
				// from the CLI today, so the supported way to unstick a
				// plan that completed under an earlier opted-in config is
				// to set allow_protected_base = true, run start to land
				// the merge, then revert the setting. State editing is the
				// fallback if the user does not want to flip global config.
				err := fmt.Errorf("plan %q recorded base %q is protected; refusing merge re-entry. To finish integrating this plan, set [project] allow_protected_base = true in springfield.toml (you can revert the setting after the merge lands)",
					planID, prior.BaseRef)
				fmt.Fprintf(w, "Error: %s\n", err.Error())
				return err
			}
		}
		_, err := runMergeIntegrationOnly(w, project, root, worktreeBase, planID)
		if err != nil {
			return err
		}
		return nil
	}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:              project,
		ControlRoot:          root,
		WorktreeBase:         worktreeBase,
		AgentIDs:             agentIDs,
		ExecutionSettings:    loaded.Config.ExecutionSettings(),
		ReviewConfig:         local.Review,
		VerifyConfig:         loaded.Config.Verify,
		Runner:               runtimeAgentRunner{coreruntime.NewRunner(registry)},
		Manager:              planrun.NewManager(),
		Progress:             w,
		EnforceProtectedBase: enforceProtected,
		TamperGuard:          &planDirTamperGuard{planDir: filepath.Join(root, ".springfield", "plans"), controlRoot: root},
		Ctx:                  ctx,
		MaxTurnsPerIteration: loaded.Config.MaxTurnsPerIteration(),
		CostCapUSD:           costCap,
	})

	if res.PlanID == "" && res.Reason == "no-eligible-plan" {
		return nil
	}
	if res.CostCapped {
		fmt.Fprintf(w, "Plan: %s\n", res.PlanID)
		fmt.Fprintf(w, "Status: cost-capped\n")
		fmt.Fprintf(w, "Est. API cost: $%.2f (cap: $%.2f)\n", res.SpendUSD, costCap)
		fmt.Fprintf(w, "Info: rerun with --cost-cap $Y to continue (Y > current spend)\n")
		// Persist run.CostCapped so the next \`springfield start\` triggers
		// the resume guard. Without this, the operator could rerun without
		// --cost-cap (or with a lower cap) and the guard would silently
		// not fire — defeating the "strictly greater than spend" contract
		// the batch path enforces. ActiveBatchID is empty because the
		// single-plan-unit path has no batch; the resume guard only
		// checks hasRun && run.CostCapped, not ActiveBatchID.
		existing, _, _ := batch.ReadRun(root)
		existing.CostCapped = true
		existing.LastCheckpoint = time.Now().UTC()
		if writeErr := batch.WriteRun(root, existing); writeErr != nil {
			fmt.Fprintf(w, "warning: persist cost-capped state: %v\n", writeErr)
		}
		return fmt.Errorf("plan %s halted by --cost-cap at $%.2f", res.PlanID, res.SpendUSD)
	}
	if res.Err != nil {
		fmt.Fprintf(w, "Plan: %s\n", res.PlanID)
		if res.Context.WorktreeRoot != "" {
			fmt.Fprintf(w, "Worktree: %s (branch %s, base %s @ %s)\n",
				res.Context.WorktreeRoot, res.Context.Branch, res.Context.BaseRef, shortSHA(res.Context.BaseHead))
		}
		if res.Status == conductor.StatusNeedsHuman {
			fmt.Fprintf(w, "Status: needs human review (%s)\n", res.Reason)
		} else {
			fmt.Fprintf(w, "Status: failed (%s)\n", res.Reason)
		}
		fmt.Fprintf(w, "Error: %s\n", res.Err.Error())
		// Match the wrap prefix to the actual terminal state. The returned
		// error is propagated into project.State.Queue.StopReason, and a
		// "plan X failed: …" prefix for a plan whose own status is `needs-human`
		// is operator-confusing (the raw state file contradicts itself).
		if res.Status == conductor.StatusNeedsHuman {
			return fmt.Errorf("plan %s needs human review: %w", res.PlanID, res.Err)
		}
		return fmt.Errorf("plan %s failed: %w", res.PlanID, res.Err)
	}

	fmt.Fprintf(w, "Plan: %s\n", res.PlanID)
	fmt.Fprintf(w, "Worktree: %s (branch %s, base %s @ %s)\n",
		res.Context.WorktreeRoot, res.Context.Branch, res.Context.BaseRef, shortSHA(res.Context.BaseHead))
	fmt.Fprintf(w, "Status: completed\n")
	if res.EvidencePath != "" {
		fmt.Fprintf(w, "Evidence: %s\n", res.EvidencePath)
	}

	mergeRes := planmerge.Integrate(planmerge.IntegrateInput{
		Project:      project,
		PlanID:       res.PlanID,
		ControlRoot:  root,
		WorktreeBase: worktreeBase,
		Progress:     w,
	})
	renderMergeOutcome(w, mergeRes)
	if mergeRes.Err != nil {
		return fmt.Errorf("plan %s merge integration failed: %w", res.PlanID, mergeRes.Err)
	}
	if mergeRes.Merge != nil && mergeRes.Merge.Status != conductor.MergeSucceeded {
		return fmt.Errorf("plan %s merge %s: %s", res.PlanID, mergeRes.Merge.Status, mergeRes.Merge.Reason)
	}
	if mergeRes.Cleanup != nil && mergeRes.Cleanup.Status == conductor.CleanupFailed {
		return fmt.Errorf("plan %s merge succeeded but cleanup failed: artifacts preserved", res.PlanID)
	}
	if mergeRes.Merge != nil && mergeRes.Merge.SourceSyncStatus == "failed" {
		return fmt.Errorf("plan %s merge succeeded but source resync failed: %s", res.PlanID, mergeRes.Merge.SourceSyncError)
	}
	return nil
}

// nextNonIntegratedCompletedPlan returns the next eligible plan ID when
// that plan already finished execution (Status=Completed) but is not yet
// integrated, signalling a re-run that should drive only the merge phase.
// Returns ("", false) when the next eligible plan is fresh (or there isn't
// one) — the caller falls through to the normal SinglePlan flow.
func nextNonIntegratedCompletedPlan(project *conductor.Project) (string, bool) {
	if project == nil || project.Config == nil {
		return "", false
	}
	schedule := conductor.BuildSchedule(project.Config)
	next := schedule.NextPlans(project.State)
	if len(next) == 0 {
		return "", false
	}
	planID := next[0]
	prior := project.State.Plans[planID]
	if prior == nil || prior.Status != conductor.StatusCompleted {
		return "", false
	}
	if prior.IsIntegrated() {
		return "", false
	}
	return planID, true
}

// runMergeIntegrationOnly drives only planmerge.Integrate for a plan whose
// execution already succeeded but whose integration is incomplete. Output
// reflects the re-entry: no fresh "Plan: ..." / "Status: completed"
// banner, just the merge phase progress and outcome.
func runMergeIntegrationOnly(w io.Writer, project *conductor.Project, root, worktreeBase, planID string) (bool, error) {
	fmt.Fprintf(w, "Plan: %s (re-running merge integration)\n", planID)
	mergeRes := planmerge.Integrate(planmerge.IntegrateInput{
		Project:      project,
		PlanID:       planID,
		ControlRoot:  root,
		WorktreeBase: worktreeBase,
		Progress:     w,
	})
	renderMergeOutcome(w, mergeRes)
	if mergeRes.Err != nil {
		return true, fmt.Errorf("plan %s merge integration failed: %w", planID, mergeRes.Err)
	}
	if mergeRes.Merge != nil && mergeRes.Merge.Status != conductor.MergeSucceeded {
		return true, fmt.Errorf("plan %s merge %s: %s", planID, mergeRes.Merge.Status, mergeRes.Merge.Reason)
	}
	if mergeRes.Cleanup != nil && mergeRes.Cleanup.Status == conductor.CleanupFailed {
		return true, fmt.Errorf("plan %s merge succeeded but cleanup failed: artifacts preserved", planID)
	}
	if mergeRes.Merge != nil && mergeRes.Merge.SourceSyncStatus == "failed" {
		return true, fmt.Errorf("plan %s merge succeeded but source resync failed: %s", planID, mergeRes.Merge.SourceSyncError)
	}
	return true, nil
}

// renderMergeOutcome prints a truthful, compact summary of the merge phase.
// Refused/failed merges surface preserved artifacts so operators see what is
// still on disk; clean success notes the published target head.
func renderMergeOutcome(w io.Writer, res planmerge.IntegrateResult) {
	if res.Merge == nil {
		return
	}
	m := res.Merge
	if m.Reason == "" {
		fmt.Fprintf(w, "Merge: %s\n", m.Status)
	} else {
		fmt.Fprintf(w, "Merge: %s (%s)\n", m.Status, m.Reason)
	}
	if m.TargetRef != "" {
		fmt.Fprintf(w, "  target: %s\n", m.TargetRef)
	}
	if m.TargetHead != "" {
		fmt.Fprintf(w, "  target head: %s\n", shortSHA(m.TargetHead))
	}
	if m.PostMergeHead != "" {
		fmt.Fprintf(w, "  post-merge head: %s\n", shortSHA(m.PostMergeHead))
	}
	if m.WorktreePath != "" && m.Status != conductor.MergeSucceeded {
		fmt.Fprintf(w, "  merge worktree (preserved): %s\n", m.WorktreePath)
	}
	if m.Error != "" {
		fmt.Fprintf(w, "  detail: %s\n", m.Error)
	}
	if res.Cleanup != nil {
		renderCleanupOutcome(w, res.Cleanup)
	}
}

func renderCleanupOutcome(w io.Writer, c *conductor.CleanupOutcome) {
	fmt.Fprintf(w, "Cleanup: %s\n", c.Status)
	pairs := []struct {
		label    string
		artifact *conductor.ArtifactCleanup
	}{
		{"merge worktree", c.MergeWorktree},
		{"execution worktree", c.ExecutionWorktree},
		{"plan branch", c.PlanBranch},
	}
	for _, p := range pairs {
		art := p.artifact
		if art == nil {
			continue
		}
		switch art.Status {
		case conductor.CleanupPreserved:
			fmt.Fprintf(w, "  %s: preserved (%s)\n", p.label, displayArtifact(art))
		case conductor.CleanupFailed:
			fmt.Fprintf(w, "  %s: cleanup failed — %s (preserved at %s)\n", p.label, art.Error, displayArtifact(art))
		}
	}
}

func displayArtifact(art *conductor.ArtifactCleanup) string {
	if art.Path != "" {
		return art.Path
	}
	return art.Branch
}

func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// planDirTamperGuard implements planrun.TamperGuard for the single-plan-unit
// path. It snapshots every file under .springfield/plans/ AND .springfield/run.json
// before each agent invocation and restores them on detected tamper. This mirrors
// the semantics of the legacy batch path's snapshotControlPlane/detectAndRecoverTamper,
// adapted for the plan-unit (non-batch) control plane layout.
type planDirTamperGuard struct {
	planDir     string
	controlRoot string            // project root; used to locate run.json
	snapshot    map[string][]byte // relpath → bytes (plan dir files)
	// runSnapshot holds the pre-agent run.json bytes, or nil if it didn't exist.
	runSnapshot   []byte
	runExistedPre bool // true when run.json existed at Snapshot time
}

func (g *planDirTamperGuard) Snapshot() error {
	tree, err := snapshotPlanDir(g.planDir)
	if err != nil {
		return fmt.Errorf("tamper snapshot: %w", err)
	}
	g.snapshot = tree

	// Snapshot run.json if controlRoot is set.
	if g.controlRoot != "" {
		runPath := batch.RunPath(g.controlRoot)
		data, err := os.ReadFile(runPath)
		if err == nil {
			g.runSnapshot = data
			g.runExistedPre = true
		} else if errors.Is(err, fs.ErrNotExist) {
			g.runSnapshot = nil
			g.runExistedPre = false
		} else {
			return fmt.Errorf("tamper snapshot run.json: %w", err)
		}
	}
	return nil
}

func (g *planDirTamperGuard) Detect() (string, error) {
	if g.snapshot == nil {
		return "", nil
	}
	current, err := snapshotPlanDir(g.planDir)
	if err != nil {
		return fmt.Sprintf("plan dir unreadable: %v", err), nil
	}
	if reason := firstTreeDivergence(g.snapshot, current, nil); reason != "" {
		return reason, nil
	}

	// Check run.json if controlRoot is configured.
	if g.controlRoot != "" {
		runPath := batch.RunPath(g.controlRoot)
		currentData, readErr := os.ReadFile(runPath)
		runExistsNow := readErr == nil
		if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
			return fmt.Sprintf("run.json unreadable: %v", readErr), nil
		}

		switch {
		case g.runExistedPre && !runExistsNow:
			return "run.json missing", nil
		case !g.runExistedPre && runExistsNow:
			return "run.json added", nil
		case g.runExistedPre && runExistsNow && !bytes.Equal(g.runSnapshot, currentData):
			return "run.json changed", nil
		}
	}
	return "", nil
}

func (g *planDirTamperGuard) Restore() error {
	if g.snapshot == nil {
		return nil
	}
	if err := os.MkdirAll(g.planDir, 0o755); err != nil {
		return fmt.Errorf("recreate plan dir: %w", err)
	}
	onDisk, err := enumeratePlanTreeRaw(g.planDir)
	if err != nil {
		return fmt.Errorf("enumerate plan dir: %w", err)
	}
	for rel := range onDisk {
		if _, keep := g.snapshot[rel]; !keep {
			abs := filepath.Join(g.planDir, filepath.FromSlash(rel))
			if rmErr := os.Remove(abs); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
				return fmt.Errorf("remove stray %s: %w", rel, rmErr)
			}
		}
	}
	for rel, data := range g.snapshot {
		abs := filepath.Join(g.planDir, filepath.FromSlash(rel))
		if err := writeFileReplacingNonRegular(abs, data, 0o644); err != nil {
			return fmt.Errorf("restore %s: %w", rel, err)
		}
	}

	// Restore run.json if controlRoot is set.
	if g.controlRoot != "" {
		runPath := batch.RunPath(g.controlRoot)
		if g.runExistedPre {
			if err := writeFileReplacingNonRegular(runPath, g.runSnapshot, 0o644); err != nil {
				return fmt.Errorf("restore run.json: %w", err)
			}
		} else {
			// run.json didn't exist before — remove any agent-created copy.
			if rmErr := os.Remove(runPath); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
				return fmt.Errorf("remove agent-created run.json: %w", rmErr)
			}
		}
	}
	return nil
}

// snapshotPlanDir walks planDir and returns a relpath→bytes map for all
// regular files. Non-existent planDir returns an empty map (no-op snapshot).
func snapshotPlanDir(planDir string) (map[string][]byte, error) {
	if _, err := os.Stat(planDir); errors.Is(err, os.ErrNotExist) {
		return make(map[string][]byte), nil
	}
	return snapshotPlanTree(planDir)
}
