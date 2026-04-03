package doctor

import (
	"context"
	"fmt"

	"springfield/internal/core/agents"
)

// Run performs readiness checks across all registered agents and returns
// a structured report suitable for CLI output or TUI rendering.
func Run(ctx context.Context, registry agents.Registry) Report {
	detections := registry.DetectAll(ctx)
	checks := make([]Check, 0, len(detections))
	available := 0

	for _, d := range detections {
		status := mapStatus(d.Status)
		if status == StatusHealthy {
			available++
		}

		checks = append(checks, Check{
			AgentID:  d.ID,
			Name:     d.Name,
			Binary:   d.Binary,
			Path:     d.Path,
			Status:   status,
			Guidance: guidance(d),
		})
	}

	healthy := available > 0
	summary := buildSummary(len(detections), available, healthy)

	return Report{
		Checks:  checks,
		Healthy: healthy,
		Summary: summary,
	}
}

func mapStatus(s agents.DetectionStatus) CheckStatus {
	switch s {
	case agents.DetectionStatusAvailable:
		return StatusHealthy
	case agents.DetectionStatusMissing:
		return StatusMissing
	default:
		return StatusUnhealthy
	}
}

func buildSummary(total, available int, healthy bool) string {
	if available == total {
		return fmt.Sprintf("All %d agent(s) available. Ready to go.", total)
	}
	if available == 0 {
		return fmt.Sprintf("No agents detected. Install at least one supported agent CLI to use Springfield.")
	}
	return fmt.Sprintf("%d/%d agent(s) available. Springfield can operate with the available agent(s).", available, total)
}
