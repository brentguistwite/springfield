package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"springfield/internal/features/batch"
	"springfield/internal/features/prd"
)

func TestSpringfieldStartHelp(t *testing.T) {
	output, err := runSpringfield(t, "start", "--help")
	if err != nil {
		t.Fatalf("start --help failed: %v\n%s", err, output)
	}

	for _, marker := range []string{
		"Execute the active Springfield batch for the current project from its saved progress.",
		"springfield plan",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected start help to contain %q, got:\n%s", marker, output)
		}
	}
}

func TestSpringfieldStartFailsWithNoBatch(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	output, err := runBinaryIn(t, bin, dir, "start")
	if err == nil {
		t.Fatalf("expected start to fail with no batch, got:\n%s", output)
	}
	if !strings.Contains(output, "springfield plan") {
		t.Fatalf("expected error to mention 'springfield plan', got:\n%s", output)
	}
}

func TestSpringfieldStatusShowsBatchState(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	// Create a batch via plan command.
	_, err := singleSlicePlan(t, bin, dir, "Implement login")
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, output)
	}

	for _, marker := range []string{
		"Batch:",
		"Title:",
		"Plans:",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected status output to contain %q, got:\n%s", marker, output)
		}
	}
}

func TestSpringfieldStatusNoStateReportsCleanly(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	output, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status with no batch should exit 0, got err=%v\n%s", err, output)
	}
	if !strings.Contains(output, "springfield init") {
		t.Errorf("expected init registration hint, got:\n%s", output)
	}
}

func TestSpringfieldStatusShowsEvidencePathForFailedSlice(t *testing.T) {
	t.Skip("TODO(phase-8) story-aware status: evidence path not surfaced in status command")
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := singleSlicePlan(t, bin, dir, "Implement login"); err != nil {
		t.Fatalf("plan failed: %v", err)
	}

	run, ok, err := batch.ReadRun(dir)
	if err != nil || !ok {
		t.Fatalf("ReadRun: ok=%v err=%v", ok, err)
	}

	fakeBinDir := filepath.Join(dir, "bin")
	installFailingAgentBinary(t, fakeBinDir, "claude")

	startOutput, startErr := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir}, "start")
	if startErr == nil {
		t.Fatalf("expected start failure, got:\n%s", startOutput)
	}

	statusOutput, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, statusOutput)
	}

	wantEvidence := filepath.Join(dir, ".springfield", "plans", run.ActiveBatchID, "evidence", "01")
	if resolved, err := filepath.EvalSymlinks(wantEvidence); err == nil {
		wantEvidence = resolved
	}
	if !strings.Contains(statusOutput, "Evidence: "+wantEvidence) {
		t.Fatalf("expected status to show evidence path %q, got:\n%s", wantEvidence, statusOutput)
	}
}

func TestSpringfieldStartRunsBatchSlices(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	// Compile a batch first.
	_, planErr := singleSlicePlan(t, bin, dir, "Implement login flow")
	if planErr != nil {
		t.Fatalf("plan failed: %v", planErr)
	}

	// Install PRD-aware fake claude binary that emits story-pass + COMPLETE.
	fakeBinDir := filepath.Join(dir, "bin")
	installPRDFakeAgentBinary(t, fakeBinDir, "claude", []string{"US-001"})

	output, err := runBinaryInWithEnv(
		t, bin, dir,
		[]string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")},
		"start",
	)
	if err != nil {
		t.Fatalf("springfield start failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Status: completed") {
		t.Fatalf("expected completed status, got:\n%s", output)
	}

	// run.json should be cleared after completion.
	runPath := filepath.Join(dir, ".springfield", "run.json")
	if _, err := os.Stat(runPath); !os.IsNotExist(err) {
		t.Error("expected run.json to be cleared after completion")
	}

	// Archive should contain the completed batch with done slices.
	archiveDir := filepath.Join(dir, ".springfield", "archive")
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected archive entry after completed batch")
	}

	archiveData, err := os.ReadFile(filepath.Join(archiveDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read archive entry: %v", err)
	}
	var archive batch.ArchiveEntry
	if err := json.Unmarshal(archiveData, &archive); err != nil {
		t.Fatalf("decode archive entry: %v", err)
	}
	_ = archive
}

// TestSpringfieldStartRecoversFromPostArchiveCrash verifies the Workstream A
// invariant: on success the archive is written first, then run.json is cleared.
// If the process dies after archive + before clear, run.json points at an
// already-archived batch id; the next springfield start must recover
// idempotently (archive already exists → skip, clear cursor, exit 0).
func TestSpringfieldStartRecoversFromPostArchiveCrash(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	if _, err := singleSlicePlan(t, bin, dir, "Implement login flow"); err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	fakeBinDir := filepath.Join(dir, "bin")
	installPRDFakeAgentBinary(t, fakeBinDir, "claude", []string{"US-001"})

	// Run to completion normally.
	if _, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start"); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	// Confirm normal completion state: archive present, no run.json.
	archiveDir := filepath.Join(dir, ".springfield", "archive")
	entries, err := os.ReadDir(archiveDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected archive entry after completion, got err=%v entries=%d", err, len(entries))
	}
	if _, err := os.Stat(filepath.Join(dir, ".springfield", "run.json")); !os.IsNotExist(err) {
		t.Fatalf("expected run.json cleared after completion, err=%v", err)
	}

	// Simulate "crash between archive and ClearRun": restore a run.json
	// pointing at the archived batch id. Archive filenames are stable:
	// <batchID>.json (single archive per id — see writeJSONExclusive).
	archivedID := ""
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".json") {
			archivedID = strings.TrimSuffix(name, ".json")
			break
		}
	}
	if archivedID == "" {
		t.Fatalf("could not extract batch id from archive entries: %v", entries)
	}
	if err := batch.WriteRun(dir, batch.Run{ActiveBatchID: archivedID}); err != nil {
		t.Fatalf("restore ghost run.json: %v", err)
	}

	// Next start: expect orphan recovery path (exits 0, clears run.json).
	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err != nil {
		t.Fatalf("expected orphan recovery to exit 0, got err=%v\n%s", err, output)
	}
	if !strings.Contains(output, "orphaned") {
		t.Errorf("expected orphan message in output, got:\n%s", output)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".springfield", "run.json")); !os.IsNotExist(statErr) {
		t.Errorf("expected run.json cleared after orphan recovery, got err=%v", statErr)
	}
}

func TestSpringfieldStartCompletionWarnsWhenArchiveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod-based write-failure test does not apply when running as root")
	}
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	if _, err := singleSlicePlan(t, bin, dir, "Implement login flow"); err != nil {
		t.Fatalf("plan failed: %v", err)
	}

	fakeBinDir := filepath.Join(dir, "bin")
	installPRDFakeAgentBinary(t, fakeBinDir, "claude", []string{"US-001"})

	// Force ArchiveBatchNormalized's MkdirAll to fail by creating a non-directory at .springfield/archive.
	archivePath := filepath.Join(dir, ".springfield", "archive")
	if err := os.MkdirAll(filepath.Join(dir, ".springfield"), 0o755); err != nil {
		t.Fatalf("mkdir .springfield: %v", err)
	}
	if err := os.WriteFile(archivePath, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("create archive collision: %v", err)
	}

	output, err := runBinaryInWithEnv(
		t, bin, dir,
		[]string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")},
		"start",
	)
	if err != nil {
		t.Fatalf("expected start to succeed (archive is best-effort), got err=%v\n%s", err, output)
	}
	if !strings.Contains(output, "Status: completed") {
		t.Errorf("expected Status: completed in output, got:\n%s", output)
	}
	if !strings.Contains(output, "warning: archive") {
		t.Errorf("expected archive warning in output, got:\n%s", output)
	}
	// Cursor was cleared (run.json gone) — that's the success signal.
	if _, ok, _ := batch.ReadRun(dir); ok {
		t.Errorf("run.json should be cleared after successful completion")
	}
}

// TestStartCommandRejectsSecondInvocationWithPid spawns two concurrent
// springfield start processes against the same root. The second one must exit
// nonzero with an error message matching:
//
//	another springfield start is already running (pid <N> since <ts>)
func TestStartCommandRejectsSecondInvocationWithPid(t *testing.T) {
	t.Skip("TODO(phase-lock) needs conductor dispatch to exercise lock contention; vacuous completion races are too fast")
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := singleSlicePlan(t, bin, dir, "Concurrent lock test"); err != nil {
		t.Fatalf("plan failed: %v", err)
	}

	// Install a fake claude that sleeps long enough to ensure the race.
	fakeBinDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(fakeBinDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	// Use /bin/sleep (absolute path) because PATH may only contain fakeBinDir
	// when the fake claude is executed, so relative `sleep` won't resolve.
	slowAgent := "#!/bin/sh\n/bin/sleep 5\necho 'agent-output'\n"
	if err := os.WriteFile(filepath.Join(fakeBinDir, "claude"), []byte(slowAgent), 0o755); err != nil {
		t.Fatalf("write slow fake claude: %v", err)
	}

	type result struct {
		out string
		err error
	}

	results := make([]result, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	startOne := func(i int) {
		defer wg.Done()
		out, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir}, "start")
		results[i] = result{out: out, err: err}
	}

	go startOne(0)
	// Wait long enough for process 0 to start, load config, and acquire the lock
	// before process 1 attempts. 500ms is generous on any CI machine.
	time.Sleep(500 * time.Millisecond)
	go startOne(1)

	wg.Wait()

	// Exactly one should fail with lock-held message.
	lockErrRe := regexp.MustCompile(`another springfield start is already running \(pid \d+ since .+\)`)
	var lockFailIdx int = -1
	for i, r := range results {
		if r.err != nil && lockErrRe.MatchString(r.out) {
			lockFailIdx = i
		}
	}
	if lockFailIdx == -1 {
		t.Errorf("expected one start to fail with lock-held message, got:\n  results[0]: err=%v out=%q\n  results[1]: err=%v out=%q",
			results[0].err, results[0].out, results[1].err, results[1].out)
	}
}

// TestRunBatchDispatchesTwoPlansInOrder compiles a 2-plan batch (plan-1 then
// plan-2, each with one user story) and verifies that both plans are dispatched
// in phase order: plan-1 appears before plan-2 in the output, and both report
// Status: completed.
func TestRunBatchDispatchesTwoPlansInOrder(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	// Build a 2-plan envelope: 2 serial phases, one plan each.
	env := prd.BatchPRDEnvelope{
		Title:  "Two plan batch",
		Source: "Two plan batch",
		Phases: []prd.PhasePRD{
			{Mode: "serial", Plans: []string{"plan-1"}},
			{Mode: "serial", Plans: []string{"plan-2"}},
		},
		Plans: []prd.BatchPRDPlan{
			{PRD: prd.PRD{
				ID:    "plan-1",
				Title: "Plan One",
				UserStories: []prd.UserStory{{
					ID:                 "US-001",
					Title:              "Story 1",
					Priority:           1,
					AcceptanceCriteria: []string{"passes"},
				}},
			}},
			{PRD: prd.PRD{
				ID:    "plan-2",
				Title: "Plan Two",
				UserStories: []prd.UserStory{{
					ID:                 "US-001",
					Title:              "Story 1",
					Priority:           1,
					AcceptanceCriteria: []string{"passes"},
				}},
			}},
		},
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if _, err := planWithPRD(t, bin, dir, string(data)); err != nil {
		t.Fatalf("plan failed: %v", err)
	}

	// Install a PRD-aware agent that passes US-001 for any plan.
	fakeBinDir := filepath.Join(dir, "bin")
	installPRDFakeAgentBinary(t, fakeBinDir, "claude", []string{"US-001"})

	output, err := runBinaryInWithEnv(
		t, bin, dir,
		[]string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")},
		"start",
	)
	if err != nil {
		t.Fatalf("springfield start failed: %v\n%s", err, output)
	}

	// Both plans must appear in the output.
	for _, id := range []string{"plan-1", "plan-2"} {
		if !strings.Contains(output, "Plan: "+id) {
			t.Errorf("expected Plan: %s in output:\n%s", id, output)
		}
	}

	// plan-1 must appear before plan-2.
	plan1Idx := strings.Index(output, "Plan: plan-1")
	plan2Idx := strings.Index(output, "Plan: plan-2")
	if plan1Idx < 0 || plan2Idx < 0 {
		t.Fatalf("could not find both plan markers in output:\n%s", output)
	}
	if plan1Idx > plan2Idx {
		t.Errorf("plan-2 appeared before plan-1 (phase order violated):\n%s", output)
	}

	// run.json cleared after completion.
	if _, statErr := os.Stat(filepath.Join(dir, ".springfield", "run.json")); !os.IsNotExist(statErr) {
		t.Error("run.json should be cleared after successful completion")
	}

	_ = batch.Run{} // keep batch import used
}
