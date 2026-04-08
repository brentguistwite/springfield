package doctor

import "springfield/internal/core/agents"

// guidance returns install/troubleshoot advice for an agent that isn't available,
// or capability notes for detection-only agents.
func guidance(d agents.Detection) string {
	switch d.Status {
	case agents.DetectionStatusMissing:
		return installGuidance(d.ID)
	case agents.DetectionStatusUnhealthy:
		return troubleshootGuidance(d)
	default:
		return capabilityNote(d.ID)
	}
}

// capabilityNote returns a note for agents that are detected but have limited capabilities.
func capabilityNote(id agents.ID) string {
	switch id {
	case agents.AgentGemini:
		return "Detection only — execution support not yet available."
	default:
		return ""
	}
}

func installGuidance(id agents.ID) string {
	switch id {
	case agents.AgentClaude:
		return "Install Claude Code: npm install -g @anthropic-ai/claude-code"
	case agents.AgentCodex:
		return "Install Codex CLI: npm install -g @openai/codex"
	case agents.AgentGemini:
		return "Install Gemini CLI: see https://github.com/google-gemini/gemini-cli. Note: detection only — execution support not yet available."
	default:
		return "Agent binary not found. Check installation docs."
	}
}

func troubleshootGuidance(d agents.Detection) string {
	msg := d.Name + " binary (" + d.Binary + ") found but not working"
	if d.Err != nil {
		msg += ": " + d.Err.Error()
	}
	msg += ". Check permissions and PATH."
	return msg
}
