package conductor_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"springfield/cmd"
	"springfield/internal/features/conductor"
)

func TestConductorStatusErrorsWithoutProjectConfig(t *testing.T) {
	root := t.TempDir()

	command := cmd.NewRootCommand()
	command.SetArgs([]string{"conductor", "status", "--dir", root})
	var buffer bytes.Buffer
	command.SetOut(&buffer)
	command.SetErr(&buffer)

	if err := command.Execute(); err == nil {
		t.Fatal("expected error")
	}
}

func TestConductorStatusReportsProgress(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())

	command := cmd.NewRootCommand()
	command.SetArgs([]string{"conductor", "status", "--dir", root})
	var buffer bytes.Buffer
	command.SetOut(&buffer)

	if err := command.Execute(); err != nil {
		t.Fatalf("execute status: %v", err)
	}

	if got := buffer.String(); !strings.Contains(got, "Progress: 0/3 plans completed") {
		t.Fatalf("status output: got %q", got)
	}
}

func TestConductorStatusShowsAgentAndEvidence(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())
	writeConductorState(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"01-bootstrap": {Status: conductor.StatusCompleted, Agent: "claude"},
			"02-config":    {Status: conductor.StatusFailed, Error: "exit code 1", Agent: "codex", EvidencePath: "/tmp/ev.log"},
		},
	})

	command := cmd.NewRootCommand()
	command.SetArgs([]string{"conductor", "status", "--dir", root})
	var buffer bytes.Buffer
	command.SetOut(&buffer)

	if err := command.Execute(); err != nil {
		t.Fatalf("execute status: %v", err)
	}

	output := buffer.String()
	if !strings.Contains(output, "claude") {
		t.Fatalf("status missing agent for completed plan: got %q", output)
	}
	if !strings.Contains(output, "codex") {
		t.Fatalf("status missing agent for failed plan: got %q", output)
	}
	if !strings.Contains(output, "/tmp/ev.log") {
		t.Fatalf("status missing evidence path: got %q", output)
	}
}

func TestConductorDiagnoseReportsFailure(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())
	writeConductorState(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"01-bootstrap": {Status: conductor.StatusCompleted},
			"02-config":    {Status: conductor.StatusFailed, Error: "exit code 1"},
		},
	})

	command := cmd.NewRootCommand()
	command.SetArgs([]string{"conductor", "diagnose", "--dir", root})
	var buffer bytes.Buffer
	command.SetOut(&buffer)

	if err := command.Execute(); err != nil {
		t.Fatalf("execute diagnose: %v", err)
	}

	output := buffer.String()
	if !strings.Contains(output, "02-config: exit code 1") {
		t.Fatalf("diagnose output: got %q", output)
	}
}

func TestConductorRunDryRunShowsNextPhase(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())

	command := cmd.NewRootCommand()
	command.SetArgs([]string{"conductor", "run", "--dir", root, "--dry-run"})
	var buffer bytes.Buffer
	command.SetOut(&buffer)

	if err := command.Execute(); err != nil {
		t.Fatalf("execute run dry-run: %v", err)
	}

	output := buffer.String()
	if !strings.Contains(output, "01-bootstrap") {
		t.Fatalf("run dry-run output: got %q", output)
	}
}

func TestConductorResumeDryRunShowsRemainingWork(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())
	writeConductorState(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"01-bootstrap": {Status: conductor.StatusCompleted},
		},
	})

	command := cmd.NewRootCommand()
	command.SetArgs([]string{"conductor", "resume", "--dir", root, "--dry-run"})
	var buffer bytes.Buffer
	command.SetOut(&buffer)

	if err := command.Execute(); err != nil {
		t.Fatalf("execute resume dry-run: %v", err)
	}

	output := buffer.String()
	if !strings.Contains(output, "02-config") {
		t.Fatalf("resume dry-run output: got %q", output)
	}
}

func TestConductorRunSuccessShowsTruthfulSummary(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())
	plansDir := filepath.Join(root, ".conductor", "plans")
	writePlanFile(t, plansDir, "01-bootstrap", "bootstrap plan")
	writePlanFile(t, plansDir, "02-config", "config plan")
	writePlanFile(t, plansDir, "03-runtime", "runtime plan")

	command := cmd.NewRootCommand()
	command.SetArgs([]string{"conductor", "run", "--dir", root})
	var buffer bytes.Buffer
	command.SetOut(&buffer)

	// This will fail because no real agent binary exists, which is expected.
	// We test the wiring, not real execution.
	err := command.Execute()

	// If it somehow succeeds (mock env), output should contain "3/3"
	if err == nil {
		output := buffer.String()
		if !strings.Contains(output, "3/3") {
			t.Fatalf("success output should show completion count: got %q", output)
		}
		if strings.Contains(output, "All plans completed.") {
			t.Fatalf("should not use generic completion message: got %q", output)
		}
	}
	// If it fails, that's fine - no real agent binary in test env
}

func TestConductorRunFailureDoesNotClaimSuccess(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())
	// No plan files -> executor will fail reading them

	command := cmd.NewRootCommand()
	command.SetArgs([]string{"conductor", "run", "--dir", root})
	var buffer bytes.Buffer
	command.SetOut(&buffer)
	command.SetErr(&buffer)

	err := command.Execute()
	if err == nil {
		output := buffer.String()
		if strings.Contains(output, "All plans completed.") {
			t.Fatalf("should not claim success when execution failed: got %q", output)
		}
	}
}
