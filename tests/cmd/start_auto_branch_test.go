package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
)

// writeEmptyActiveBatch installs an active batch with no plans so runBatch
// returns vacuously after auto-branch has fired. Used by auto-branch tests
// that want to exercise just the branch lifecycle without standing up an
// agent and full merge flow.
func writeEmptyActiveBatch(t *testing.T, root, batchID, title string) {
	t.Helper()
	paths, err := batch.NewPaths(root, batchID)
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}
	b := batch.Batch{
		ID:      batchID,
		Title:   title,
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{}}},
		PlanIDs: []string{},
	}
	if err := batch.WriteBatch(paths, b, "source", nil); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: batchID}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
}

func TestAutoBranchHappyPathOnProtectedBase(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfigStrict(t, dir, "claude")
	writeEmptyActiveBatch(t, dir, "batch-auto-1", "Auto Branch Test")

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	output, err := runBinaryIn(t, bin, dir, "start")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, output)
	}

	wantSnippets := []string{
		"auto-cut branch springfield/batch-batch-auto-1 from main",
		"slice work merges here; you stay on main",
		"batch complete on springfield/batch-batch-auto-1 (you are on main)",
		"git push -u origin springfield/batch-batch-auto-1",
	}
	for _, want := range wantSnippets {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q in output:\n%s", want, output)
		}
	}

	// The checkout must never have left main — the auto-branch is created as a
	// bare ref, not switched onto. HEAD still points at main's tip.
	if got := strings.TrimSpace(gitOut(t, dir, "branch", "--show-current")); got != "main" {
		t.Fatalf("expected still on main, got %q", got)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "rev-parse", "main")); got != strings.TrimSpace(gitOut(t, dir, "rev-parse", "springfield/batch-batch-auto-1")) {
		t.Fatalf("auto-branch must be cut from main's tip (no divergence when batch is empty)")
	}
	branches := gitOut(t, dir, "branch", "--list", "springfield/batch-batch-auto-1")
	if strings.TrimSpace(branches) == "" {
		t.Fatalf("expected auto-cut branch to be retained, got empty list")
	}
}

func TestAutoBranchRefusesDirtyTree(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfigStrict(t, dir, "claude")
	writeEmptyActiveBatch(t, dir, "batch-auto-2", "Auto Branch Test")

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	// Introduce a dirty tracked file (modified after commit).
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# repo\nDIRTY\n"), 0o644); err != nil {
		t.Fatalf("dirty: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "start")
	if err == nil {
		t.Fatalf("expected start to refuse on dirty tree, got success:\n%s", output)
	}
	if !strings.Contains(output, "uncommitted changes") {
		t.Errorf("expected uncommitted-changes error in output:\n%s", output)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "branch", "--show-current")); got != "main" {
		t.Fatalf("must remain on main on dirty refusal, got %q", got)
	}
	if br := strings.TrimSpace(gitOut(t, dir, "branch", "--list", "springfield/batch-batch-auto-2")); br != "" {
		t.Fatalf("auto-branch must not be created on dirty refusal, got %q", br)
	}
}

func TestAutoBranchAppendsSuffixOnCollision(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfigStrict(t, dir, "claude")
	writeEmptyActiveBatch(t, dir, "batch-auto-3", "Auto Branch Test")

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	// Pre-create a collision: the default rendered name and the -2 suffix.
	gitMust(t, dir, "branch", "springfield/batch-batch-auto-3")
	gitMust(t, dir, "branch", "springfield/batch-batch-auto-3-2")

	output, err := runBinaryIn(t, bin, dir, "start")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, output)
	}
	if !strings.Contains(output, "auto-cut branch springfield/batch-batch-auto-3-3 from main") {
		t.Fatalf("expected -3 suffix branch, got:\n%s", output)
	}
}

func TestAutoBranchSkippedOnFeatureBranch(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfigStrict(t, dir, "claude")
	writeEmptyActiveBatch(t, dir, "batch-auto-4", "Auto Branch Test")

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")
	// Move off main onto a feature branch so the auto-branch logic stays a no-op.
	gitMust(t, dir, "switch", "-c", "feat/manual")

	output, err := runBinaryIn(t, bin, dir, "start")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, output)
	}
	if strings.Contains(output, "auto-cut branch") {
		t.Fatalf("auto-cut must not fire on a feature branch, output:\n%s", output)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "branch", "--show-current")); got != "feat/manual" {
		t.Fatalf("expected to stay on feat/manual, got %q", got)
	}
}

func TestAutoBranchOptOutWithAllowProtected(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	// Lenient config: auto_branch=false + allow_protected_base=true → batch
	// runs directly on main without an auto-cut.
	writeSpringfieldConfig(t, dir, "claude")
	writeEmptyActiveBatch(t, dir, "batch-auto-5", "Auto Branch Test")

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	output, err := runBinaryIn(t, bin, dir, "start")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, output)
	}
	if strings.Contains(output, "auto-cut branch") {
		t.Fatalf("auto-cut must not fire when auto_branch=false:\n%s", output)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "branch", "--show-current")); got != "main" {
		t.Fatalf("expected to stay on main, got %q", got)
	}
}
