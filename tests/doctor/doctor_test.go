package doctor_test

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
	"springfield/internal/features/doctor"
)

func TestRunReportsAllHealthyWhenAllAvailable(t *testing.T) {
	lookPath := func(binary string) (string, error) {
		return "/usr/local/bin/" + binary, nil
	}

	registry := agents.NewRegistry(
		claude.New(lookPath),
		codex.New(lookPath),
		gemini.New(lookPath),
	)

	report := doctor.Run(context.Background(), registry)

	if !report.Healthy {
		t.Fatal("expected healthy report when all agents available")
	}

	if len(report.Checks) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(report.Checks))
	}

	for _, check := range report.Checks {
		if check.Status != doctor.StatusHealthy {
			t.Fatalf("expected healthy status for %q, got %q", check.AgentID, check.Status)
		}
		// Gemini is detection-only, so it gets a capability note even when healthy.
		if check.AgentID == agents.AgentGemini {
			if check.Guidance == "" {
				t.Fatal("expected detection-only guidance for gemini")
			}
			continue
		}
		if check.Guidance != "" {
			t.Fatalf("expected no guidance for healthy agent %q, got %q", check.AgentID, check.Guidance)
		}
	}
}

func TestRunReportsMissingWithInstallGuidance(t *testing.T) {
	lookPath := func(binary string) (string, error) {
		return "", exec.ErrNotFound
	}

	registry := agents.NewRegistry(
		claude.New(lookPath),
		codex.New(lookPath),
		gemini.New(lookPath),
	)

	report := doctor.Run(context.Background(), registry)

	if report.Healthy {
		t.Fatal("expected unhealthy report when all agents missing")
	}

	for _, check := range report.Checks {
		if check.Status != doctor.StatusMissing {
			t.Fatalf("expected missing status for %q, got %q", check.AgentID, check.Status)
		}
		if check.Guidance == "" {
			t.Fatalf("expected install guidance for missing agent %q", check.AgentID)
		}
	}
}

func TestRunReportsUnhealthyWithTroubleshootGuidance(t *testing.T) {
	lookPath := func(binary string) (string, error) {
		return "", errors.New("permission denied")
	}

	registry := agents.NewRegistry(
		claude.New(lookPath),
	)

	report := doctor.Run(context.Background(), registry)

	if report.Healthy {
		t.Fatal("expected unhealthy report")
	}

	check := report.Checks[0]
	if check.Status != doctor.StatusUnhealthy {
		t.Fatalf("expected unhealthy status, got %q", check.Status)
	}
	if check.Guidance == "" {
		t.Fatal("expected troubleshoot guidance for unhealthy agent")
	}
}

func TestRunReportsMixedEnvironment(t *testing.T) {
	lookPath := func(binary string) (string, error) {
		if binary == "claude" {
			return "/usr/local/bin/claude", nil
		}
		return "", exec.ErrNotFound
	}

	registry := agents.NewRegistry(
		claude.New(lookPath),
		codex.New(lookPath),
		gemini.New(lookPath),
	)

	report := doctor.Run(context.Background(), registry)

	// At least one agent available — healthy enough to operate.
	if !report.Healthy {
		t.Fatal("expected healthy when at least one agent available")
	}

	if report.Checks[0].Status != doctor.StatusHealthy {
		t.Fatalf("expected claude healthy, got %q", report.Checks[0].Status)
	}
	if report.Checks[1].Status != doctor.StatusMissing {
		t.Fatalf("expected codex missing, got %q", report.Checks[1].Status)
	}
	// Missing agents should still have guidance
	if report.Checks[1].Guidance == "" {
		t.Fatal("expected guidance for missing codex")
	}
}

func TestRunSummaryDescribesOverallState(t *testing.T) {
	lookPath := func(binary string) (string, error) {
		return "/usr/local/bin/" + binary, nil
	}

	registry := agents.NewRegistry(
		claude.New(lookPath),
		codex.New(lookPath),
		gemini.New(lookPath),
	)

	report := doctor.Run(context.Background(), registry)

	if report.Summary == "" {
		t.Fatal("expected non-empty summary")
	}
}
