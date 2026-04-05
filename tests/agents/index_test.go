package agents_test

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
)

func TestRegistryResolveUsesProjectDefaultWithoutOverride(t *testing.T) {
	registry := newRegistry()

	resolved, err := registry.Resolve(agents.ResolveInput{
		ProjectDefault: agents.AgentCodex,
	})
	if err != nil {
		t.Fatalf("resolve adapter: %v", err)
	}

	if resolved.Source != agents.ResolveSourceProjectDefault {
		t.Fatalf("expected project default source, got %q", resolved.Source)
	}

	if resolved.Adapter.ID() != agents.AgentCodex {
		t.Fatalf("expected codex adapter, got %q", resolved.Adapter.ID())
	}
}

func TestRegistryResolveUsesPlanOverrideWhenPresent(t *testing.T) {
	registry := newRegistry()

	resolved, err := registry.Resolve(agents.ResolveInput{
		ProjectDefault: agents.AgentClaude,
		PlanOverride:   agents.AgentGemini,
	})
	if err != nil {
		t.Fatalf("resolve adapter: %v", err)
	}

	if resolved.Source != agents.ResolveSourcePlanOverride {
		t.Fatalf("expected plan override source, got %q", resolved.Source)
	}

	if resolved.Adapter.ID() != agents.AgentGemini {
		t.Fatalf("expected gemini adapter, got %q", resolved.Adapter.ID())
	}
}

func TestRegistryResolveRejectsUnsupportedAgent(t *testing.T) {
	registry := newRegistry()

	_, err := registry.Resolve(agents.ResolveInput{
		ProjectDefault: agents.ID("unknown"),
	})
	if !errors.Is(err, agents.ErrUnsupportedAgent) {
		t.Fatalf("expected unsupported agent error, got %v", err)
	}
}

func TestRegistryDetectReportsAgentMetadataAndAvailability(t *testing.T) {
	lookPath := func(binary string) (string, error) {
		switch binary {
		case "claude":
			return "/opt/bin/claude", nil
		case "codex":
			return "", exec.ErrNotFound
		case "gemini":
			return "", errors.New("permission denied")
		default:
			t.Fatalf("unexpected binary lookup %q", binary)
			return "", nil
		}
	}

	registry := agents.NewRegistry(
		claude.New(lookPath),
		codex.New(lookPath),
		gemini.New(lookPath),
	)

	results := registry.DetectAll(context.Background())

	if len(results) != 3 {
		t.Fatalf("expected 3 detection results, got %d", len(results))
	}

	assertDetection(t, results[0], agents.AgentClaude, "Claude Code", "claude", agents.DetectionStatusAvailable, "/opt/bin/claude")
	assertDetection(t, results[1], agents.AgentCodex, "Codex CLI", "codex", agents.DetectionStatusMissing, "")
	assertDetection(t, results[2], agents.AgentGemini, "Gemini CLI", "gemini", agents.DetectionStatusUnhealthy, "")
}

func newRegistry() agents.Registry {
	return agents.NewRegistry(
		claude.New(exec.LookPath),
		codex.New(exec.LookPath),
		gemini.New(exec.LookPath),
	)
}

func assertDetection(
	t *testing.T,
	result agents.Detection,
	id agents.ID,
	name string,
	binary string,
	status agents.DetectionStatus,
	path string,
) {
	t.Helper()

	if result.ID != id {
		t.Fatalf("expected id %q, got %q", id, result.ID)
	}

	if result.Name != name {
		t.Fatalf("expected name %q, got %q", name, result.Name)
	}

	if result.Binary != binary {
		t.Fatalf("expected binary %q, got %q", binary, result.Binary)
	}

	if result.Status != status {
		t.Fatalf("expected status %q, got %q", status, result.Status)
	}

	if result.Path != path {
		t.Fatalf("expected path %q, got %q", path, result.Path)
	}
}
