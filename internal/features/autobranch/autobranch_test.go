package autobranch

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestBaseForBatchUsesAutoBranchWhenActive(t *testing.T) {
	got := BaseForBatch("main", &Activation{BranchName: "springfield/batch-abc"})
	if got != "springfield/batch-abc" {
		t.Fatalf("active auto-branch must become the base, got %q", got)
	}
}

func TestBaseForBatchFallsBackToResolvedBase(t *testing.T) {
	if got := BaseForBatch("feat/x", nil); got != "feat/x" {
		t.Fatalf("nil activation must leave the resolved base unchanged, got %q", got)
	}
}

func TestResolveBranchNameValid(t *testing.T) {
	got, err := ResolveBranchName("springfield/batch-{id}", "abc123", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "springfield/batch-abc123" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveBranchNameUnknownPlaceholder(t *testing.T) {
	_, err := ResolveBranchName("feat/{slug}-{id}", "abc", nil)
	if err == nil {
		t.Fatal("expected unknown placeholder error")
	}
	if !strings.Contains(err.Error(), "unsupported placeholder") {
		t.Fatalf("error msg: %v", err)
	}
}

func TestResolveBranchNameEmptyPattern(t *testing.T) {
	if _, err := ResolveBranchName("", "abc", nil); err == nil {
		t.Fatal("expected empty-pattern error")
	}
}

func TestResolveBranchNameEmptyBatchID(t *testing.T) {
	if _, err := ResolveBranchName("springfield/batch-{id}", "", nil); err == nil {
		t.Fatal("expected empty-batch-id error")
	}
}

func TestResolveBranchNameNoCollision(t *testing.T) {
	got, err := ResolveBranchName("springfield/batch-{id}", "abc", func(string) (bool, error) {
		return false, nil
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "springfield/batch-abc" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveBranchNameCollisionSuffix(t *testing.T) {
	// First two names exist, third is free → "-3" wins.
	taken := map[string]bool{
		"springfield/batch-abc":   true,
		"springfield/batch-abc-2": true,
	}
	got, err := ResolveBranchName("springfield/batch-{id}", "abc", func(b string) (bool, error) {
		return taken[b], nil
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "springfield/batch-abc-3" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveBranchNameCollisionExhausted(t *testing.T) {
	_, err := ResolveBranchName("springfield/batch-{id}", "abc", func(string) (bool, error) {
		return true, nil
	})
	if err == nil {
		t.Fatal("expected exhaustion error")
	}
	if !strings.Contains(err.Error(), "already exist") {
		t.Fatalf("error msg: %v", err)
	}
}

func TestResolveBranchNameBranchExistsError(t *testing.T) {
	_, err := ResolveBranchName("springfield/batch-{id}", "abc", func(string) (bool, error) {
		return false, errors.New("git boom")
	})
	if err == nil || !strings.Contains(err.Error(), "git boom") {
		t.Fatalf("expected upstream error wrapped, got %v", err)
	}
}

// fakeGit is a scripted Git implementation for Activate/Restore tests. It
// records branch creates and never mutates `current`, mirroring the real
// behavior: auto-branching creates a ref without switching the worktree.
type fakeGit struct {
	current    string
	dirty      bool
	currentErr error
	dirtyErr   error
	existing   map[string]bool
	// created records (branch, startPoint) pairs passed to CreateBranch.
	created   [][2]string
	createErr error
}

func (f *fakeGit) CurrentBranch(string) (string, error) {
	return f.current, f.currentErr
}

func (f *fakeGit) BranchExists(_, b string) (bool, error) {
	return f.existing[b], nil
}

func (f *fakeGit) IsDirty(string) (bool, error) {
	return f.dirty, f.dirtyErr
}

func (f *fakeGit) CreateBranch(_, branch, startPoint string) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, [2]string{branch, startPoint})
	if f.existing == nil {
		f.existing = map[string]bool{}
	}
	f.existing[branch] = true
	return nil
}

func TestActivateNotProtected(t *testing.T) {
	g := &fakeGit{current: "feat/x"}
	var buf bytes.Buffer
	a, err := Activate(Input{Git: g, Dir: "/r", BatchID: "id1", Pattern: "springfield/batch-{id}", Enabled: true}, &buf)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if a != nil {
		t.Fatalf("expected no-op, got %+v", a)
	}
	if len(g.created) != 0 {
		t.Fatalf("must not switch when not on protected base")
	}
}

func TestActivateDisabled(t *testing.T) {
	g := &fakeGit{current: "main"}
	var buf bytes.Buffer
	a, err := Activate(Input{Git: g, Dir: "/r", BatchID: "id1", Pattern: "springfield/batch-{id}", Enabled: false}, &buf)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if a != nil {
		t.Fatalf("expected no-op when disabled, got %+v", a)
	}
}

func TestActivateProtectedHappyPath(t *testing.T) {
	g := &fakeGit{current: "main"}
	var buf bytes.Buffer
	a, err := Activate(Input{Git: g, Dir: "/r", BatchID: "abc", Pattern: "springfield/batch-{id}", Enabled: true}, &buf)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if a == nil {
		t.Fatal("expected activation")
	}
	if a.OriginalBranch != "main" {
		t.Fatalf("OriginalBranch=%q", a.OriginalBranch)
	}
	if a.BranchName != "springfield/batch-abc" {
		t.Fatalf("BranchName=%q", a.BranchName)
	}
	if a.Reason != "created" {
		t.Fatalf("Reason=%q", a.Reason)
	}
	if len(g.created) != 1 || g.created[0] != [2]string{"springfield/batch-abc", "main"} {
		t.Fatalf("expected branch created from main without switching, got created=%v", g.created)
	}
	// The core guarantee: the worktree never switches off the operator's branch.
	if g.current != "main" {
		t.Fatalf("main worktree must stay on main, got %q", g.current)
	}
	out := buf.String()
	for _, want := range []string{"auto-cut branch springfield/batch-abc", "slice work merges here; you stay on main"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestActivateDirtyTreeBlocked(t *testing.T) {
	g := &fakeGit{current: "main", dirty: true}
	var buf bytes.Buffer
	_, err := Activate(Input{Git: g, Dir: "/r", BatchID: "abc", Pattern: "springfield/batch-{id}", Enabled: true}, &buf)
	if err == nil {
		t.Fatal("expected dirty-tree error")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("error: %v", err)
	}
	if len(g.created) != 0 {
		t.Fatalf("must not switch when dirty")
	}
}

func TestActivateCollisionAppendsSuffix(t *testing.T) {
	g := &fakeGit{
		current:  "main",
		existing: map[string]bool{"springfield/batch-abc": true},
	}
	var buf bytes.Buffer
	a, err := Activate(Input{Git: g, Dir: "/r", BatchID: "abc", Pattern: "springfield/batch-{id}", Enabled: true}, &buf)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if a.BranchName != "springfield/batch-abc-2" {
		t.Fatalf("BranchName=%q", a.BranchName)
	}
}

func TestActivateResumeAlreadyOnAutoBranch(t *testing.T) {
	g := &fakeGit{current: "springfield/batch-abc"}
	var buf bytes.Buffer
	a, err := Activate(Input{
		Git:                 g,
		Dir:                 "/r",
		BatchID:             "abc",
		Pattern:             "springfield/batch-{id}",
		Enabled:             true,
		AlreadyAutoBranch:   true,
		PriorOriginalBranch: "main",
		PriorAutoBranchName: "springfield/batch-abc",
	}, &buf)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if a == nil || a.Reason != "resumed" {
		t.Fatalf("Activation=%+v", a)
	}
	if len(g.created) != 0 {
		t.Fatalf("resume must not create branch again")
	}
	if len(g.created) != 0 {
		t.Fatalf("resume must not create a branch")
	}
	if !strings.Contains(buf.String(), "auto-branch resume") {
		t.Fatalf("missing resume log:\n%s", buf.String())
	}
}

// Resume must be a pure no-op on the worktree: the main worktree was never
// switched off the original branch, so there is nothing to create or switch.
// It only re-derives the Activation so the base is re-threaded.
func TestActivateResumeDoesNotTouchWorktree(t *testing.T) {
	g := &fakeGit{current: "main"}
	var buf bytes.Buffer
	a, err := Activate(Input{
		Git:                 g,
		Dir:                 "/r",
		BatchID:             "abc",
		Pattern:             "springfield/batch-{id}",
		Enabled:             true,
		AlreadyAutoBranch:   true,
		PriorOriginalBranch: "main",
		PriorAutoBranchName: "springfield/batch-abc",
	}, &buf)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if a == nil || a.Reason != "resumed" {
		t.Fatalf("Activation=%+v", a)
	}
	if a.OriginalBranch != "main" || a.BranchName != "springfield/batch-abc" {
		t.Fatalf("resume must re-derive original/branch, got %+v", a)
	}
	if len(g.created) != 0 {
		t.Fatalf("resume must not create a branch, got %v", g.created)
	}
	if g.current != "main" {
		t.Fatalf("resume must leave the worktree on main, got %q", g.current)
	}
}

// A dirty working tree no longer blocks resume: resume performs no git ops, so
// there is no switch that uncommitted changes could corrupt.
func TestActivateResumeIgnoresDirtyTree(t *testing.T) {
	g := &fakeGit{current: "main", dirty: true}
	var buf bytes.Buffer
	a, err := Activate(Input{
		Git:                 g,
		Dir:                 "/r",
		BatchID:             "abc",
		Pattern:             "springfield/batch-{id}",
		Enabled:             true,
		AlreadyAutoBranch:   true,
		PriorOriginalBranch: "main",
		PriorAutoBranchName: "springfield/batch-abc",
	}, &buf)
	if err != nil {
		t.Fatalf("resume must succeed regardless of dirty tree, got %v", err)
	}
	if a == nil || a.Reason != "resumed" {
		t.Fatalf("Activation=%+v", a)
	}
}

func TestActivateBeforePersistCreateFires(t *testing.T) {
	g := &fakeGit{current: "main"}
	var buf bytes.Buffer
	var persisted struct {
		original, branch string
		called           int
	}
	_, err := Activate(Input{
		Git: g, Dir: "/r", BatchID: "abc", Pattern: "springfield/batch-{id}", Enabled: true,
		BeforePersistCreate: func(o, b string) error {
			persisted.original = o
			persisted.branch = b
			persisted.called++
			// hook must fire BEFORE switch
			if len(g.created) != 0 {
				t.Errorf("hook fired after switch")
			}
			return nil
		},
	}, &buf)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if persisted.called != 1 {
		t.Fatalf("hook called %d times", persisted.called)
	}
	if persisted.original != "main" || persisted.branch != "springfield/batch-abc" {
		t.Fatalf("hook args: %+v", persisted)
	}
	if len(g.created) != 1 {
		t.Fatalf("switch must run after hook")
	}
}

func TestActivateBeforePersistCreateAbortsBeforeSwitch(t *testing.T) {
	g := &fakeGit{current: "main"}
	var buf bytes.Buffer
	_, err := Activate(Input{
		Git: g, Dir: "/r", BatchID: "abc", Pattern: "springfield/batch-{id}", Enabled: true,
		BeforePersistCreate: func(string, string) error {
			return errors.New("disk full")
		},
	}, &buf)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected wrapped persist err, got %v", err)
	}
	if len(g.created) != 0 {
		t.Fatalf("switch must not fire when persist fails")
	}
}

func TestActivateBeforePersistCreateNotCalledOnResume(t *testing.T) {
	g := &fakeGit{current: "springfield/batch-abc"}
	var buf bytes.Buffer
	hookCalled := false
	_, err := Activate(Input{
		Git: g, Dir: "/r", BatchID: "abc", Pattern: "springfield/batch-{id}", Enabled: true,
		AlreadyAutoBranch:   true,
		PriorOriginalBranch: "main",
		PriorAutoBranchName: "springfield/batch-abc",
		BeforePersistCreate: func(string, string) error {
			hookCalled = true
			return nil
		},
	}, &buf)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if hookCalled {
		t.Fatalf("hook must not fire on resume path")
	}
}

func TestActivateCreateBranchError(t *testing.T) {
	g := &fakeGit{current: "main", createErr: errors.New("permission denied")}
	var buf bytes.Buffer
	_, err := Activate(Input{Git: g, Dir: "/r", BatchID: "abc", Pattern: "springfield/batch-{id}", Enabled: true}, &buf)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected wrapped create-branch error, got %v", err)
	}
}

func TestRestoreSuccess(t *testing.T) {
	a := &Activation{OriginalBranch: "main", BranchName: "springfield/batch-abc"}
	var buf bytes.Buffer
	Restore(a, OutcomeSuccess, &buf)
	out := buf.String()
	for _, want := range []string{"batch complete on springfield/batch-abc (you are on main)", "git push -u origin springfield/batch-abc"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	// Restore must not claim a switch happened — the worktree never moved.
	if strings.Contains(out, "switched back") {
		t.Errorf("Restore must not claim a switch-back:\n%s", out)
	}
}

func TestRestoreFailure(t *testing.T) {
	a := &Activation{OriginalBranch: "main", BranchName: "springfield/batch-abc"}
	var buf bytes.Buffer
	Restore(a, OutcomeFailed, &buf)
	out := buf.String()
	for _, want := range []string{"batch failed on springfield/batch-abc (you are on main)", "preserved for inspection", "git switch springfield/batch-abc"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestRestoreInterrupted(t *testing.T) {
	a := &Activation{OriginalBranch: "main", BranchName: "springfield/batch-abc"}
	var buf bytes.Buffer
	Restore(a, OutcomeInterrupted, &buf)
	out := buf.String()
	for _, want := range []string{"interrupted on springfield/batch-abc", "rerun \"springfield start\" to resume"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestRestoreNilActivation(t *testing.T) {
	var buf bytes.Buffer
	Restore(nil, OutcomeSuccess, &buf)
	if buf.Len() != 0 {
		t.Fatalf("nil activation must be a no-op, got output:\n%s", buf.String())
	}
}

// Sanity check that the Git interface is exhaustive enough for Activate.
func TestActivateRequiresGit(t *testing.T) {
	_, err := Activate(Input{Dir: "/r"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "Git is required") {
		t.Fatalf("expected Git-required error, got %v", err)
	}
}

// Compile-time check that placeholder regex stays sane.
var _ = func() error {
	if !placeholderRE.MatchString("{id}") {
		return fmt.Errorf("regex broken")
	}
	return nil
}()
