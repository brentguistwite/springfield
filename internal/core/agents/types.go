package agents

import (
	"context"
	"errors"

	"springfield/internal/core/exec"
)

type ID string

const (
	AgentClaude ID = "claude"
	AgentCodex  ID = "codex"
	AgentGemini ID = "gemini"
)

type Capability string

type CapabilitySet map[Capability]bool

type Metadata struct {
	ID           ID
	Name         string
	Binary       string
	Capabilities CapabilitySet
}

type DetectionStatus string

const (
	DetectionStatusAvailable DetectionStatus = "available"
	DetectionStatusMissing   DetectionStatus = "missing"
	DetectionStatusUnhealthy DetectionStatus = "unhealthy"
)

type Detection struct {
	ID     ID
	Name   string
	Binary string
	Status DetectionStatus
	Path   string
	Err    error
}

type Adapter interface {
	ID() ID
	Metadata() Metadata
	Detect(context.Context) Detection
}

type LookPathFunc func(string) (string, error)

type ResolveInput struct {
	ProjectDefault ID
	PlanOverride   ID
}

type ResolveSource string

const (
	ResolveSourceProjectDefault ResolveSource = "project_default"
	ResolveSourcePlanOverride   ResolveSource = "plan_override"
)

type Resolved struct {
	Adapter Adapter
	Source  ResolveSource
}

// CommandInput provides the parameters needed to build an agent CLI invocation.
type CommandInput struct {
	Prompt  string
	WorkDir string
}

// Commander extends Adapter with the ability to produce a runnable command spec.
type Commander interface {
	Adapter
	Command(input CommandInput) exec.Command
}

var ErrUnsupportedAgent = errors.New("unsupported agent")
