package conductor_test

import (
	"bytes"
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
