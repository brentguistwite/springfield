package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"springfield/internal/core/config"
	"springfield/internal/features/batch"
	"springfield/internal/features/execution"
	"springfield/internal/features/workflow"
)

// NewStartCommand runs the active Springfield batch from its saved progress.
func NewStartCommand() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Execute the active Springfield batch for the current project from its saved progress.",
		Long:  "Execute the active Springfield batch for the current project from its saved progress.\n\nRun \"springfield plan\" first to compile a batch.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := config.LoadFrom(dir)
			if err != nil {
				return err
			}
			root := loaded.RootDir

			run, hasRun, err := batch.ReadRun(root)
			if err != nil {
				return err
			}

			if !hasRun || run.ActiveBatchID == "" {
				return fmt.Errorf("no Springfield plan found for this repo — run \"springfield plan\" first")
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
			fmt.Fprintf(w, "Phase: %d of %d\n", run.ActivePhaseIdx+1, len(b.Phases))
			if logPath != "" {
				fmt.Fprintf(w, "Log: %s\n", logPath)
			}

			result, execErr := runBatch(root, run, b, w, logPath)

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

			// Atomic archive write is durable before we clear the cursor, so
			// archive first. If the process dies between archive-rename and
			// ClearRun, the next start sees run.json pointing at an already-
			// archived id and RecoverOrphan handles it idempotently.
			if archiveErr := batch.ArchiveBatchNormalized(root, b, "completed"); archiveErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: archive completed batch %q: %v\n", b.ID, archiveErr)
			}
			if clearErr := batch.ClearRun(root); clearErr != nil {
				return fmt.Errorf("clear run state after completion: %w", clearErr)
			}

			fmt.Fprintf(w, "Status: completed\n")
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	return cmd
}

// BatchRunResult summarizes the outcome of running a batch.
type BatchRunResult struct {
	Status string
	Error  string
	// RunStateCleared is true when runBatch has already archived+cleared the
	// run cursor on an unrecoverable path (e.g. tamper detection). The caller
	// must not re-write run.json, or the cleared cursor gets stranded again.
	RunStateCleared bool
}

func runBatch(root string, run batch.Run, b batch.Batch, progress io.Writer, logPath string) (BatchRunResult, error) {
	runner, err := workflow.NewRuntimeRunner(root, exec.LookPath, nil)
	if err != nil {
		return BatchRunResult{Error: err.Error()}, err
	}

	phase, ok := b.ActivePhase(run.ActivePhaseIdx)
	if !ok {
		return BatchRunResult{Status: "completed"}, nil
	}

	batchPaths, pathErr := batch.NewPaths(root, b.ID)
	if pathErr != nil {
		return BatchRunResult{Error: pathErr.Error()}, pathErr
	}

	for _, sliceID := range phase.Slices {
		s, found := b.SliceByID(sliceID)
		if !found {
			return BatchRunResult{Error: fmt.Sprintf("slice %q not found in batch", sliceID)}, nil
		}
		if s.Status == batch.SliceDone {
			continue
		}

		fmt.Fprintf(progress, "  slice %s start — %s\n", s.ID, s.Title)

		s.Status = batch.SliceRunning
		if err := batch.UpdateBatchSlice(batchPaths, s); err != nil {
			return BatchRunResult{Error: err.Error()}, err
		}

		// Snapshot the entire Springfield control plane before the agent
		// runs: batch.json, run.json, source.md. The agent is not expected
		// to touch any of them; any byte-level difference is tamper.
		snap, snapErr := snapshotControlPlane(root, batchPaths)
		if snapErr != nil {
			return BatchRunResult{Error: fmt.Sprintf("snapshot control plane: %v", snapErr)}, snapErr
		}

		report, runErr := runner.Executor.Run(root, sliceToExecutionWork(b, s))

		forensics := tamperForensicsContext{
			batchID:      b.ID,
			sliceID:      s.ID,
			agentID:      report.AgentID,
			agentLogPath: logPath,
			exitCode:     report.ExitCode,
		}
		if tamperErr := detectAndRecoverTamper(root, batchPaths, snap, forensics); tamperErr != nil {
			return BatchRunResult{Error: tamperErr.Error(), RunStateCleared: true}, tamperErr
		}

		if runErr != nil || report.Status == "failed" {
			s.Status = batch.SliceFailed
			s.Error = report.Error
			if runErr != nil && s.Error == "" {
				s.Error = runErr.Error()
			}
			if err := batch.UpdateBatchSlice(batchPaths, s); err != nil {
				return BatchRunResult{Error: s.Error}, fmt.Errorf("%s; also failed to persist slice status: %w", s.Error, err)
			}
			fmt.Fprintf(progress, "  slice %s failed — %s\n", s.ID, s.Error)
			return BatchRunResult{Error: s.Error}, runErr
		}

		s.Status = batch.SliceDone
		if err := batch.UpdateBatchSlice(batchPaths, s); err != nil {
			return BatchRunResult{Error: err.Error()}, err
		}
		fmt.Fprintf(progress, "  slice %s done\n", s.ID)
	}

	return BatchRunResult{Status: "completed"}, nil
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

// snapshotTmpPrefix matches scratch files produced by writeFileAtomic's
// os.CreateTemp pattern ("tmp-<base>-*"). Those files exist only for the
// duration of an atomic rename and must not be captured by the snapshot.
const snapshotTmpPrefix = ".tmp-"

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

// snapshotPlanTree walks planDir and returns a relpath->bytes map. Tmp
// scratch files are skipped. Missing planDir is an error (the caller has
// just written batch.json into it).
//
// Non-regular entries (symlinks, devices, fifos, sockets) are rejected:
// Springfield only writes regular files under the plan dir, so any other
// node is an integrity violation. Reads use O_NOFOLLOW as defense-in-depth
// against a symlink being swapped in after the d.Type() check.
func snapshotPlanTree(planDir string) (map[string][]byte, error) {
	out := make(map[string][]byte)
	err := filepath.WalkDir(planDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, snapshotTmpPrefix) {
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
		data, err := io.ReadAll(f)
		closeErr := f.Close()
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		if closeErr != nil {
			return fmt.Errorf("close %s: %w", rel, closeErr)
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
	reason := compareControlPlane(root, paths, snap)
	if reason == "" {
		return nil
	}

	// Capture post-tamper bytes before restore overwrites them.
	postBytes := capturePostBytesForReason(root, paths, reason)

	var restoreErr, archiveErr error
	if err := restoreControlPlane(root, paths, snap); err != nil {
		restoreErr = err
	} else {
		batchBytes := snap.tree["batch.json"]
		var restored batch.Batch
		if err := json.Unmarshal(batchBytes, &restored); err == nil {
			if err := batch.ArchiveBatchNormalized(root, restored, "state-tampered"); err != nil {
				archiveErr = err
			}
		}
	}

	// Sidecar is best-effort forensic only; don't fail the caller if it
	// can't land. Write after archive so archive dir exists.
	preBytes := preBytesForReason(snap, reason)
	if sidecarErr := writeTamperSidecar(root, forensics, reason, preBytes, postBytes); sidecarErr != nil {
		// Swallow: a missing sidecar should never escalate past a tamper
		// message that already tells the operator what happened.
		_ = sidecarErr
	}

	_ = batch.ClearRun(root)

	msg := fmt.Sprintf("state tampered by agent (%s)", reason)
	if restoreErr != nil {
		msg += fmt.Sprintf("; restore failed: %v", restoreErr)
	}
	if archiveErr != nil {
		msg += fmt.Sprintf("; archive failed: %v", archiveErr)
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
// Tmp scratch files are skipped. Used by restoreControlPlane to find stray
// nodes — including non-regular ones planted by the agent — so they can be
// unlinked before restore rewrites.
func enumeratePlanTreeRaw(planDir string) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	err := filepath.WalkDir(planDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, snapshotTmpPrefix) {
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
func compareControlPlane(root string, paths batch.Paths, snap controlPlaneSnapshot) string {
	current, err := snapshotPlanTree(paths.PlanDir())
	if err != nil {
		return "plan dir unreadable"
	}
	if reason := firstTreeDivergence(snap.tree, current); reason != "" {
		return reason
	}
	runNow, err := os.ReadFile(batch.RunPath(root))
	if err != nil {
		return "run.json missing"
	}
	if !bytes.Equal(runNow, snap.runBytes) {
		return "run.json changed"
	}
	return ""
}

// firstTreeDivergence compares two relpath->bytes maps and returns a reason
// string identifying the first divergent relpath, or "" when they match.
// Iteration is sorted so the reason is deterministic across runs.
func firstTreeDivergence(want, got map[string][]byte) string {
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

// sliceToExecutionWork converts a batch slice into an execution.Work for the runtime adapter.
func sliceToExecutionWork(b batch.Batch, s batch.Slice) execution.Work {
	return execution.Work{
		ID:    b.ID + "-" + s.ID,
		Title: s.Title,
		Split: "single",
		Workstreams: []execution.Workstream{
			{
				Name:    s.ID,
				Title:   s.Title,
				Summary: s.Summary,
			},
		},
	}
}
