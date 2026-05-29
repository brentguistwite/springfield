package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/execution"
	"springfield/internal/features/prd"
)

const legacyPayloadSnippetLen = 200

// NewPlanCommand compiles a Springfield batch from a caller-provided PRD envelope.
func NewPlanCommand() *cobra.Command {
	var dir string
	var prdArg string
	var fromDir string
	var replace bool
	var appendMode bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Compile a Springfield plan into a runnable batch.",
		Long: "Compile a Springfield plan from a caller-provided PRD envelope.\n\n" +
			"Use --prd <path> to read a JSON envelope from a file, or --prd - to read from stdin.\n" +
			"Use --from-dir <path> to load <path>/batch.json as the envelope.\n" +
			"Use --dry-run to preview the compiled batch without writing to .springfield/.\n" +
			"The springfield:plan skill emits this envelope. Run \"springfield start\" to execute.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Mutual exclusion: --prd and --from-dir cannot both be set.
			if prdArg != "" && fromDir != "" {
				return fmt.Errorf("--prd and --from-dir are mutually exclusive; use one or the other")
			}

			if prdArg == "" && fromDir == "" {
				return fmt.Errorf("--prd is required (path to PRD envelope JSON, or \"-\" for stdin)")
			}

			// Resolve payload source: --from-dir reads <path>/batch.json.
			if fromDir != "" {
				prdArg = filepath.Join(fromDir, "batch.json")
			}

			// Load project root.
			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}
			rootDir := loaded.RootDir

			// Read payload bytes.
			payload, err := readPayload(prdArg)
			if err != nil {
				return err
			}

			// Two-pass legacy detection: check for "slices" key before strict decode.
			if isLegacySlicePayload(payload) {
				return fmt.Errorf(
					"legacy single-slice batch detected; this format is no longer supported. " +
						"Re-author with the PRD shape (see docs/prd-format.md).",
				)
			}

			// Strict parse via prd.ParseEnvelope.
			env, err := prd.ParseEnvelope(bytes.NewReader(payload))
			if err != nil {
				snippet := payload
				if len(snippet) > legacyPayloadSnippetLen {
					snippet = snippet[:legacyPayloadSnippetLen]
				}
				return fmt.Errorf("parse PRD envelope: %w\n(first %d bytes: %s)", err, len(snippet), snippet)
			}

			// Dry-run: short-circuit before any mutation. Compile, print summary,
			// return. Composable with --replace/--append (previews the operation).
			if dryRun {
				return runDryRun(cmd, rootDir, env, replace, appendMode)
			}

			// Ensure execution config exists.
			if err := execution.EnsureExecutionConfig(rootDir); err != nil {
				return err
			}

			// Load conductor project for config access.
			project, err := conductor.LoadProjectRaw(rootDir)
			if err != nil {
				return fmt.Errorf("load conductor project: %w", err)
			}

			// Handle active batch.
			run, hasRun, err := batch.ReadRun(rootDir)
			if err != nil {
				return fmt.Errorf("read run state: %w", err)
			}
			var priorBatch *batch.Batch
			if hasRun && run.ActiveBatchID != "" {
				paths, err := batch.NewPaths(rootDir, run.ActiveBatchID)
				if err != nil {
					return fmt.Errorf("resolve batch paths: %w", err)
				}
				b, err := batch.ReadBatch(paths)
				if err == nil {
					priorBatch = &b
				} else if !batch.IsMissingBatchError(err) {
					return fmt.Errorf("read active batch: %w", err)
				}
			}

			if priorBatch != nil {
				switch {
				case replace:
					// Compile and validate the new envelope FIRST so we catch all
					// envelope bugs before touching the existing batch. This ensures
					// the prior batch is never archived unless the replacement is valid.
					replaceOut, err := batch.Compile(batch.CompileInput{
						Envelope:          env,
						RegisteredPlanIDs: registeredPlanIDs(project),
					})
					if err != nil {
						return err
					}

					// Refuse only if a RUNNING plan is being REMOVED by this --replace.
					// A running plan whose unit is preserved in the new envelope is not
					// affected — its registration stays and the runner's state record is
					// untouched. Blocking those would be needlessly conservative; batch
					// abort uses the same narrow shape (only blocks on b.PlanIDs).
					newIDsForGuard := make(map[string]struct{}, len(replaceOut.Units))
					for _, unit := range replaceOut.Units {
						newIDsForGuard[unit.ID] = struct{}{}
					}
					for planID, ps := range project.State.Plans {
						if ps == nil || ps.Status != conductor.StatusRunning {
							continue
						}
						if _, kept := newIDsForGuard[planID]; kept {
							continue
						}
						return fmt.Errorf(
							"plan %q is currently running (status=running) and would be REMOVED by --replace; wait for it to finish or run \"springfield recover\" first",
							planID,
						)
					}

					// Surface warnings to stderr before any mutation.
					for _, w := range replaceOut.Warnings {
						fmt.Fprintf(cmd.ErrOrStderr(), "[warn] %s\n", w)
					}

					if err := batch.ArchiveBatchNormalized(rootDir, *priorBatch, "replaced"); err != nil {
						return fmt.Errorf("archive prior batch: %w", err)
					}
					// Clear run.json immediately after archive so there is no window
					// where run.json still points at the now-deleted prior batch.
					// The brief no-active-batch window is acceptable here.
					if err := batch.ClearRun(rootDir); err != nil {
						return fmt.Errorf("clear run after archive: %w", err)
					}
					// --replace is a full reset: remove every registered plan unit whose
					// ID is not in the new envelope, not just the prior batch's IDs. The
					// registry can drift from the active batch (standalone "plans add", or
					// a partially-failed earlier replace), and a stale unit holding an order
					// slot would collide with the new units below ("order N already used").
					// Snapshot stale IDs first — RemovePlanUnit mutates the slice we range.
					newIDs := make(map[string]struct{}, len(replaceOut.Units))
					for _, unit := range replaceOut.Units {
						newIDs[unit.ID] = struct{}{}
					}
					var staleIDs []string
					for _, u := range project.Config.PlanUnits {
						if _, keep := newIDs[u.ID]; !keep {
							staleIDs = append(staleIDs, u.ID)
						}
					}
					for _, id := range staleIDs {
						_ = project.RemovePlanUnit(id) // best-effort; may not be registered
					}
					priorBatch = nil

					// Write the pre-compiled batch (already validated above).
					// Any failure here is after archive — hint the operator.
					replacePaths, err := batch.NewPaths(rootDir, replaceOut.Batch.ID)
					if err != nil {
						return fmt.Errorf("resolve batch paths (write failed after archive — re-run \"springfield plan --replace --prd ...\" to rebuild from the new envelope (springfield recover alone will NOT clean up — run.json is gone, so it will report no-op and leave stale plan units behind)): %w", err)
					}
					if err := batch.WriteBatch(replacePaths, replaceOut.Batch, replaceOut.Source, replaceOut.Plans); err != nil {
						return fmt.Errorf("write batch (write failed after archive — re-run \"springfield plan --replace --prd ...\" to rebuild from the new envelope (springfield recover alone will NOT clean up — run.json is gone, so it will report no-op and leave stale plan units behind)): %w", err)
					}
					// Snapshot already-registered units by ID. AddPlanUnit rejects
					// duplicates by design (the "plans add" UX needs that guarantee),
					// so a preserved unit (same ID in both old and new envelopes)
					// cannot be re-added — it would either error or, worse, succeed
					// silently and leave the old Path/Order/Title in place if a future
					// implementation bypassed the dedup. To keep --replace semantics
					// honest, drop-and-re-add when ANY field drifted; skip when the
					// existing record already matches the new envelope.
					existing := make(map[string]conductor.PlanUnit, len(project.Config.PlanUnits))
					for _, u := range project.Config.PlanUnits {
						existing[u.ID] = u
					}
					sameUnit := func(a, b conductor.PlanUnit) bool {
						return a.Title == b.Title && a.Path == b.Path && a.Order == b.Order
					}
					for _, unit := range replaceOut.Units {
						if prior, already := existing[unit.ID]; already {
							if sameUnit(prior, unit) {
								continue
							}
							// Drift in Title/Path/Order — drop the stale record so the
							// new envelope's values take effect. RemovePlanUnit on a
							// known-registered ID returns nil; ignoring its error here
							// is safe because the next AddPlanUnit call would surface
							// the same condition.
							_ = project.RemovePlanUnit(unit.ID)
						}
						if _, err := project.AddPlanUnit(conductor.PlanUnitInput{
							ID:    unit.ID,
							Title: unit.Title,
							Path:  unit.Path,
							Order: unit.Order,
						}); err != nil {
							return fmt.Errorf("register plan unit %q (write failed after archive — re-run \"springfield plan --replace --prd ...\" to rebuild from the new envelope (springfield recover alone will NOT clean up — run.json is gone, so it will report no-op and leave stale plan units behind)): %w", unit.ID, err)
						}
					}
					if err := project.SaveConfig(); err != nil {
						return fmt.Errorf("save execution config (write failed after archive — re-run \"springfield plan --replace --prd ...\" to rebuild from the new envelope (springfield recover alone will NOT clean up — run.json is gone, so it will report no-op and leave stale plan units behind)): %w", err)
					}
					newRun := batch.Run{
						ActiveBatchID:  replaceOut.Batch.ID,
						ActivePlanIDs:  nil,
						LastCheckpoint: time.Now().UTC(),
					}
					if err := batch.WriteRun(rootDir, newRun); err != nil {
						return fmt.Errorf("write run.json (write failed after archive — re-run \"springfield plan --replace --prd ...\" to rebuild from the new envelope (springfield recover alone will NOT clean up — run.json is gone, so it will report no-op and leave stale plan units behind)): %w", err)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Compiled batch %q with %d plan(s).\n", replaceOut.Batch.ID, len(replaceOut.Plans))
					return nil

				case appendMode:
					// Refuse if any plan is currently running. Append registers new
					// units alongside the running batch's runner-owned state record;
					// adding units while the runner is mid-flight is conservative but
					// keeps the registry's order-slot accounting from racing with the
					// runner's MarkPassed/MarkFailed writes.
					for planID, ps := range project.State.Plans {
						if ps != nil && ps.Status == conductor.StatusRunning {
							return fmt.Errorf(
								"plan %q is currently running (status=running); wait for it to finish or run \"springfield recover\" first",
								planID,
							)
						}
					}
					return runAppend(cmd, rootDir, project, *priorBatch, run, env)

				default:
					return fmt.Errorf(
						"active batch %q exists; use --replace to archive it or --append to add new plans",
						priorBatch.ID,
					)
				}
			}

			// Compile new batch.
			out, err := batch.Compile(batch.CompileInput{
				Envelope:          env,
				RegisteredPlanIDs: registeredPlanIDs(project),
			})
			if err != nil {
				return err
			}

			// Surface warnings to stderr.
			for _, w := range out.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "[warn] %s\n", w)
			}

			// Write batch to disk.
			paths, err := batch.NewPaths(rootDir, out.Batch.ID)
			if err != nil {
				return fmt.Errorf("resolve batch paths: %w", err)
			}
			if err := batch.WriteBatch(paths, out.Batch, out.Source, out.Plans); err != nil {
				return fmt.Errorf("write batch: %w", err)
			}

			// Register plan units in conductor config.
			for _, unit := range out.Units {
				if _, err := project.AddPlanUnit(conductor.PlanUnitInput{
					ID:    unit.ID,
					Title: unit.Title,
					Path:  unit.Path,
					Order: unit.Order,
				}); err != nil {
					return fmt.Errorf("register plan unit %q: %w", unit.ID, err)
				}
			}

			if err := project.SaveConfig(); err != nil {
				return fmt.Errorf("save execution config: %w", err)
			}

			// Write run.json cursor.
			newRun := batch.Run{
				ActiveBatchID:  out.Batch.ID,
				ActivePlanIDs:  nil,
				LastCheckpoint: time.Now().UTC(),
			}
			if err := batch.WriteRun(rootDir, newRun); err != nil {
				return fmt.Errorf("write run.json: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Compiled batch %q with %d plan(s).\n", out.Batch.ID, len(out.Plans))
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	cmd.Flags().StringVar(&prdArg, "prd", "", "path to PRD envelope JSON, or \"-\" to read from stdin")
	cmd.Flags().StringVar(&fromDir, "from-dir", "", "directory containing batch.json (operator-authored PRD); mutually exclusive with --prd")
	cmd.Flags().BoolVar(&replace, "replace", false, "archive the current active batch and replace it with this one")
	cmd.Flags().BoolVar(&appendMode, "append", false, "add new plans to the end of the current active batch")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview the compiled batch without writing to .springfield/")

	return cmd
}

// runDryRun compiles the envelope and prints a summary without writing any
// state under .springfield/. Composes with --replace and --append (previews
// the operation, including collision detection for append).
func runDryRun(cmd *cobra.Command, rootDir string, env prd.BatchPRDEnvelope, replace, appendMode bool) error {
	project, err := conductor.LoadProjectRaw(rootDir)
	if err != nil {
		return fmt.Errorf("load conductor project: %w", err)
	}

	run, hasRun, err := batch.ReadRun(rootDir)
	if err != nil {
		return fmt.Errorf("read run state: %w", err)
	}

	var existingIDs map[string]struct{}
	if hasRun && run.ActiveBatchID != "" {
		if !replace && !appendMode {
			fmt.Fprintf(cmd.ErrOrStderr(), "[warn] active batch %q exists; --dry-run does not modify it.\n", run.ActiveBatchID)
		}
		if appendMode {
			paths, perr := batch.NewPaths(rootDir, run.ActiveBatchID)
			if perr != nil {
				return fmt.Errorf("resolve batch paths: %w", perr)
			}
			b, berr := batch.ReadBatch(paths)
			if berr == nil {
				existingIDs = make(map[string]struct{}, len(b.PlanIDs))
				for _, id := range b.PlanIDs {
					existingIDs[id] = struct{}{}
				}
				for _, p := range env.Plans {
					if _, clash := existingIDs[p.ID]; clash {
						return fmt.Errorf("append failed: plan %q already exists in batch %q", p.ID, b.ID)
					}
				}
			} else if !batch.IsMissingBatchError(berr) {
				return fmt.Errorf("read active batch: %w", berr)
			}
		}
	}

	out, err := batch.Compile(batch.CompileInput{
		Envelope:          env,
		ExistingIDs:       existingIDs,
		RegisteredPlanIDs: registeredPlanIDs(project),
	})
	if err != nil {
		return err
	}

	for _, w := range out.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "[warn] %s\n", w)
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Dry run: would compile batch %q with %d plan(s).\n", out.Batch.ID, len(out.Plans))
	fmt.Fprintln(w, "Phases:")
	for i, ph := range out.Batch.Phases {
		mode := "serial"
		if ph.Mode == batch.PhaseParallel {
			mode = "parallel"
		}
		fmt.Fprintf(w, "  %d. [%s] %s\n", i+1, mode, joinIDs(ph.Plans))
	}
	fmt.Fprintf(w, "Plan IDs (first-seen order): %s\n", joinIDs(out.Batch.PlanIDs))
	if len(out.Warnings) > 0 {
		fmt.Fprintf(w, "Validation warnings (printed to stderr): %d\n", len(out.Warnings))
	}
	return nil
}

func joinIDs(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	out := ids[0]
	for _, id := range ids[1:] {
		out += ", " + id
	}
	return out
}

// runAppend adds plans from the new envelope to an existing batch.
func runAppend(cmd *cobra.Command, rootDir string, project *conductor.Project, prior batch.Batch, run batch.Run, env prd.BatchPRDEnvelope) error {
	// Build collision check.
	existingIDs := make(map[string]struct{}, len(prior.PlanIDs))
	for _, id := range prior.PlanIDs {
		existingIDs[id] = struct{}{}
	}

	// Compile only the new envelope's plans — reject if any collide.
	for _, p := range env.Plans {
		if _, exists := existingIDs[p.ID]; exists {
			return fmt.Errorf("append failed: plan %q already exists in batch %q", p.ID, prior.ID)
		}
	}

	// Compile new envelope to get WrittenPlans and Units.
	newOut, err := batch.Compile(batch.CompileInput{
		Envelope:          env,
		ExistingIDs:       existingIDs,
		RegisteredPlanIDs: registeredPlanIDs(project),
	})
	if err != nil {
		return err
	}

	// Surface warnings.
	for _, w := range newOut.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "[warn] %s\n", w)
	}

	// Merge plan IDs into existing batch and rewrite batch.json.
	prior.PlanIDs = append(prior.PlanIDs, newOut.Batch.PlanIDs...)
	for _, ph := range newOut.Batch.Phases {
		prior.Phases = append(prior.Phases, ph)
	}

	paths, err := batch.NewPaths(rootDir, prior.ID)
	if err != nil {
		return fmt.Errorf("resolve batch paths: %w", err)
	}

	// Preserve the original source.md — the append path must NOT overwrite it
	// with the new envelope's source, or the original batch provenance is lost.
	originalSourceBytes, readErr := os.ReadFile(paths.SourcePath())
	if readErr != nil {
		return fmt.Errorf("read existing source.md: %w", readErr)
	}

	// Rewrite batch.json (source stays from original batch).
	if err := batch.WriteBatch(paths, prior, string(originalSourceBytes), newOut.Plans); err != nil {
		return fmt.Errorf("write appended batch: %w", err)
	}

	// Register new plan units; use Order=0 so AddPlanUnit assigns next slot
	// (avoids collision with existing units from the prior batch).
	for _, unit := range newOut.Units {
		if _, err := project.AddPlanUnit(conductor.PlanUnitInput{
			ID:    unit.ID,
			Title: unit.Title,
			Path:  unit.Path,
			Order: 0, // auto-assign next available order
		}); err != nil {
			return fmt.Errorf("register plan unit %q: %w", unit.ID, err)
		}
	}

	if err := project.SaveConfig(); err != nil {
		return fmt.Errorf("save execution config: %w", err)
	}

	// Update run.json cursor (keep active batch ID unchanged).
	run.LastCheckpoint = time.Now().UTC()
	if err := batch.WriteRun(rootDir, run); err != nil {
		return fmt.Errorf("write run.json: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Appended %d plan(s) to batch %q.\n", len(newOut.Plans), prior.ID)
	return nil
}

// readPayload reads the full payload bytes from a file path or stdin ("-").
func readPayload(src string) ([]byte, error) {
	if src == "-" {
		return io.ReadAll(os.Stdin)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, fmt.Errorf("read PRD file %q: %w", src, err)
	}
	return data, nil
}

// registeredPlanIDs returns the set of plan unit IDs currently registered in the
// conductor config. Used to prevent a new batch ID from colliding with a standalone
// registered plan's directory under .springfield/plans/<id>/.
func registeredPlanIDs(project *conductor.Project) map[string]struct{} {
	ids := make(map[string]struct{}, len(project.Config.PlanUnits))
	for _, u := range project.Config.PlanUnits {
		ids[u.ID] = struct{}{}
	}
	return ids
}

// isLegacySlicePayload does a lenient pre-decode into map[string]json.RawMessage
// and checks if the "slices" key exists with an array value. This must run before
// prd.ParseEnvelope so the error message is human-readable rather than opaque.
func isLegacySlicePayload(data []byte) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false // not even valid JSON; let ParseEnvelope handle it
	}
	slicesVal, ok := raw["slices"]
	if !ok {
		return false
	}
	// Verify the value is an array.
	var arr []json.RawMessage
	return json.Unmarshal(slicesVal, &arr) == nil
}
