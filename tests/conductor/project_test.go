package conductor_test

import (
	"errors"
	"os"
	"testing"

	"springfield/internal/core/config"
	"springfield/internal/features/conductor"
)

func TestLoadProjectReadsConfigFromSpringfieldRuntime(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	if project.Config.Tool != "claude" {
		t.Fatalf("tool: got %q want claude", project.Config.Tool)
	}

	if got := project.AllPlans(); len(got) != 3 || got[0] != "01-bootstrap" || got[2] != "03-runtime" {
		t.Fatalf("all plans: got %v", got)
	}
}

func TestLoadProjectUsesRepoRootWhenStartedNested(t *testing.T) {
	root := t.TempDir()
	nested := root + "/plans/release"
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())

	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	project, err := conductor.LoadProject(nested)
	if err != nil {
		t.Fatalf("load project from nested dir: %v", err)
	}

	if got := project.AllPlans()[0]; got != "01-bootstrap" {
		t.Fatalf("first plan: got %q want 01-bootstrap", got)
	}
}

func TestLoadProjectRequiresSpringfieldConfig(t *testing.T) {
	root := t.TempDir()

	_, err := conductor.LoadProject(root)
	if err == nil {
		t.Fatal("expected missing config error")
	}

	var missing *config.MissingConfigError
	if !errors.As(err, &missing) {
		t.Fatalf("expected MissingConfigError, got %T", err)
	}
}

func TestLoadProjectRequiresConductorConfig(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)

	_, err := conductor.LoadProject(root)
	if err == nil {
		t.Fatal("expected missing conductor config error")
	}
}

func TestSaveStateRoundTrips(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	project.MarkCompleted("01-bootstrap")
	project.MarkFailed("02-config", "timeout")
	if err := project.SaveState(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload project: %v", err)
	}

	if got := reloaded.PlanStatus("01-bootstrap"); got != conductor.StatusCompleted {
		t.Fatalf("01-bootstrap status: got %q want %q", got, conductor.StatusCompleted)
	}

	if got := reloaded.PlanError("02-config"); got != "timeout" {
		t.Fatalf("02-config error: got %q want timeout", got)
	}
}

func TestAllPlansFlattensSequentialThenBatches(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, mixedConfig())

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	want := []string{"seq-1", "seq-2", "batch-a", "batch-b", "batch-c"}
	got := project.AllPlans()
	if len(got) != len(want) {
		t.Fatalf("all plans length: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("all plans[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}
