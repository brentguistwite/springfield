package doctor

import "springfield/internal/core/agents"

// guidance returns install/troubleshoot advice for an agent that isn't available.
func guidance(d agents.Detection) string {
	switch d.Status {
	case agents.DetectionStatusMissing:
		return installGuidance(d.ID)
	case agents.DetectionStatusUnhealthy:
		return troubleshootGuidance(d)
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
		return "Install Gemini CLI: npm install -g @anthropic-ai/gemini-cli (or see https://github.com/google-gemini/gemini-cli)"
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
