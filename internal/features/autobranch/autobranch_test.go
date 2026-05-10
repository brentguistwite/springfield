package autobranch

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

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

// fakeGit is a scripted Git implementation for Activate/Restore tests.
type fakeGit struct {
	current      string
	dirty        bool
	currentErr   error
	dirtyErr     error
	existing     map[string]bool
	switchedTo   []string
	switchCreate []string
	switchErr    error
	createErr    error
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

func (f *fakeGit) SwitchCreate(_, b string) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.switchCreate = append(f.switchCreate, b)
	f.current = b
	if f.existing == nil {
		f.existing = map[string]bool{}
	}
	f.existing[b] = true
	return nil
}

func (f *fakeGit) Switch(_, b string) error {
	if f.switchErr != nil {
		return f.switchErr
	}
	f.switchedTo = append(f.switchedTo, b)
	f.current = b
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
	if len(g.switchCreate) != 0 {
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
	if len(g.switchCreate) != 1 || g.switchCreate[0] != "springfield/batch-abc" {
		t.Fatalf("switchCreate=%v", g.switchCreate)
	}
	out := buf.String()
	for _, want := range []string{"auto-cut branch springfield/batch-abc", "all slice work will merge here", "switching back to main"} {
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
	if len(g.switchCreate) != 0 {
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
	if len(g.switchCreate) != 0 {
		t.Fatalf("resume must not create branch again")
	}
	if len(g.switchedTo) != 0 {
		t.Fatalf("resume must not switch when already on auto-branch")
	}
	if !strings.Contains(buf.String(), "auto-branch resume") {
		t.Fatalf("missing resume log:\n%s", buf.String())
	}
}

func TestActivateResumeFromOriginalSwitchesBack(t *testing.T) {
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
	if len(g.switchedTo) != 1 || g.switchedTo[0] != "springfield/batch-abc" {
		t.Fatalf("expected switch to auto-branch on resume, got %v", g.switchedTo)
	}
}

func TestActivateResumeDirtyBlocked(t *testing.T) {
	g := &fakeGit{current: "main", dirty: true}
	var buf bytes.Buffer
	_, err := Activate(Input{
		Git:                 g,
		Dir:                 "/r",
		BatchID:             "abc",
		Pattern:             "springfield/batch-{id}",
		Enabled:             true,
		AlreadyAutoBranch:   true,
		PriorOriginalBranch: "main",
		PriorAutoBranchName: "springfield/batch-abc",
	}, &buf)
	if err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("expected dirty refusal on resume, got %v", err)
	}
}

func TestActivateSwitchCreateError(t *testing.T) {
	g := &fakeGit{current: "main", createErr: errors.New("permission denied")}
	var buf bytes.Buffer
	_, err := Activate(Input{Git: g, Dir: "/r", BatchID: "abc", Pattern: "springfield/batch-{id}", Enabled: true}, &buf)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected wrapped switch error, got %v", err)
	}
}

func TestRestoreSuccess(t *testing.T) {
	g := &fakeGit{current: "springfield/batch-abc"}
	a := &Activation{OriginalBranch: "main", BranchName: "springfield/batch-abc"}
	var buf bytes.Buffer
	if err := Restore(g, "/r", a, OutcomeSuccess, &buf); err != nil {
		t.Fatalf("err: %v", err)
	}
	if g.switchedTo[0] != "main" {
		t.Fatalf("switchedTo=%v", g.switchedTo)
	}
	out := buf.String()
	for _, want := range []string{"batch complete on springfield/batch-abc", "switched back to main", "git push -u origin springfield/batch-abc"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestRestoreFailure(t *testing.T) {
	g := &fakeGit{current: "springfield/batch-abc"}
	a := &Activation{OriginalBranch: "main", BranchName: "springfield/batch-abc"}
	var buf bytes.Buffer
	if err := Restore(g, "/r", a, OutcomeFailed, &buf); err != nil {
		t.Fatalf("err: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"batch failed on springfield/batch-abc", "preserved for inspection", "git switch springfield/batch-abc"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestRestoreInterrupted(t *testing.T) {
	g := &fakeGit{current: "springfield/batch-abc"}
	a := &Activation{OriginalBranch: "main", BranchName: "springfield/batch-abc"}
	var buf bytes.Buffer
	if err := Restore(g, "/r", a, OutcomeInterrupted, &buf); err != nil {
		t.Fatalf("err: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"interrupted on springfield/batch-abc", "rerun \"springfield start\" to resume"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestRestoreSwitchBackFails(t *testing.T) {
	g := &fakeGit{current: "springfield/batch-abc", switchErr: errors.New("dirty index")}
	a := &Activation{OriginalBranch: "main", BranchName: "springfield/batch-abc"}
	var buf bytes.Buffer
	err := Restore(g, "/r", a, OutcomeSuccess, &buf)
	if err == nil {
		t.Fatal("expected error when switch back fails")
	}
	if !strings.Contains(buf.String(), "failed to switch back") {
		t.Fatalf("missing remediation msg:\n%s", buf.String())
	}
}

func TestRestoreNilActivation(t *testing.T) {
	if err := Restore(&fakeGit{}, "/r", nil, OutcomeSuccess, &bytes.Buffer{}); err != nil {
		t.Fatalf("nil activation must be no-op, got %v", err)
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
