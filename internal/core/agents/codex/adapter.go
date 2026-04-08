package codex

import (
	"context"
	"errors"
	"os/exec"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
)

type adapter struct {
	lookPath agents.LookPathFunc
}

func New(lookPath agents.LookPathFunc) agents.Commander {
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	return adapter{lookPath: lookPath}
}

func (a adapter) ID() agents.ID {
	return agents.AgentCodex
}

func (a adapter) Metadata() agents.Metadata {
	return agents.Metadata{
		ID:           agents.AgentCodex,
		Name:         "Codex CLI",
		Binary:       "codex",
		Capabilities: agents.CapabilitySet{},
	}
}

func (a adapter) Detect(context.Context) agents.Detection {
	metadata := a.Metadata()
	path, err := a.lookPath(metadata.Binary)

	result := agents.Detection{
		ID:     metadata.ID,
		Name:   metadata.Name,
		Binary: metadata.Binary,
		Path:   path,
		Err:    err,
	}

	switch {
	case err == nil:
		result.Status = agents.DetectionStatusAvailable
	case errors.Is(err, exec.ErrNotFound):
		result.Status = agents.DetectionStatusMissing
	default:
		result.Status = agents.DetectionStatusUnhealthy
	}

	return result
}

func (a adapter) Command(input agents.CommandInput) coreexec.Command {
	args := []string{"exec", "--json"}
	if input.ExecutionSettings.Codex.SandboxMode != "" {
		args = append(args, "-s", input.ExecutionSettings.Codex.SandboxMode)
	}
	if input.ExecutionSettings.Codex.ApprovalPolicy != "" {
		args = append(args, "-a", input.ExecutionSettings.Codex.ApprovalPolicy)
	}
	args = append(args, input.Prompt)

	return coreexec.Command{
		Name: "codex",
		Args: args,
		Dir:  input.WorkDir,
	}
}
