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

// capabilityNote returns a note for agents that are detected but have limited
// capabilities. All built-in agents are execution-supported as of 2026-04, so
// this returns an empty string today; kept as an extension point for future
// detection-only adapters.
func capabilityNote(id agents.ID) string {
	_ = id
	return ""
}

func installGuidance(id agents.ID) string {
	switch id {
	case agents.AgentClaude:
		return "Install Claude Code: npm install -g @anthropic-ai/claude-code"
	case agents.AgentCodex:
		return "Install Codex CLI: npm install -g @openai/codex"
	case agents.AgentGemini:
		return "Install Gemini CLI: see https://github.com/google-gemini/gemini-cli"
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
