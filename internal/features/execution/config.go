package execution

import (
	"springfield/internal/features/conductor"
)

const (
	LocalPlansDir   = conductor.LocalPlansDir
	TrackedPlansDir = conductor.TrackedPlansDir
)

const legacyTrackedPlansDir = ".conductor/plans"

// Defaults returns Springfield-owned execution defaults.
func Defaults() Input {
	defaults := conductor.SetupDefaults()
	return Input{
		PlansDir:                   defaults.PlansDir,
		WorktreeBase:               defaults.WorktreeBase,
		MaxRetries:                 defaults.MaxRetries,
		SingleWorkstreamIterations: defaults.SingleWorkstreamIterations,
		SingleWorkstreamTimeout:    defaults.SingleWorkstreamTimeout,
	}
}

// IsReady reports whether Springfield execution config already exists.
func IsReady(rootDir string) (bool, error) {
	return conductor.IsReady(rootDir)
}

// IsTrackedPlansDir accepts both Springfield-owned and legacy tracked plan locations.
func IsTrackedPlansDir(plansDir string) bool {
	return plansDir == TrackedPlansDir || plansDir == legacyTrackedPlansDir
}

// Load reads execution config through Springfield's public execution boundary.
func Load(rootDir string) (*Config, error) {
	project, err := conductor.LoadProject(rootDir)
	if err != nil {
		return nil, err
	}

	return &Config{
		PlansDir:                   project.Config.PlansDir,
		WorktreeBase:               project.Config.WorktreeBase,
		MaxRetries:                 project.Config.MaxRetries,
		SingleWorkstreamIterations: project.Config.SingleWorkstreamIterations,
		SingleWorkstreamTimeout:    project.Config.SingleWorkstreamTimeout,
		Tool:                       project.Config.Tool,
		FallbackTool:               project.Config.FallbackTool,
		Batches:                    project.Config.Batches,
		Sequential:                 project.Config.Sequential,
	}, nil
}

// Setup creates execution config using Springfield-owned inputs.
func Setup(rootDir string, priority []string, input Input) (Result, error) {
	result, err := conductor.Setup(rootDir, toConductorOptions(priority, input))
	if err != nil {
		return Result{}, err
	}
	return Result{
		Created: result.Created,
		Reused:  result.Reused,
		Path:    result.Path,
	}, nil
}

// Update updates existing execution config using Springfield-owned inputs.
func Update(rootDir string, priority []string, input Input) (UpdateResult, error) {
	result, err := conductor.UpdateConfig(rootDir, toConductorOptions(priority, input))
	if err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{
		Updated: result.Updated,
		Path:    result.Path,
	}, nil
}

func toConductorOptions(priority []string, input Input) conductor.SetupOptions {
	defaults := conductor.SetupDefaults()
	primary, fallback := toolsFromPriority(priority)
	defaults.Tool = primary
	defaults.FallbackTool = fallback
	defaults.PlansDir = input.PlansDir
	defaults.WorktreeBase = input.WorktreeBase
	defaults.MaxRetries = input.MaxRetries
	defaults.SingleWorkstreamIterations = input.SingleWorkstreamIterations
	defaults.SingleWorkstreamTimeout = input.SingleWorkstreamTimeout
	return defaults
}

func toolsFromPriority(priority []string) (string, string) {
	primary := ""
	for _, candidate := range priority {
		if candidate == "" {
			continue
		}
		primary = candidate
		break
	}
	if primary == "" {
		return "", ""
	}
	for _, candidate := range priority {
		if candidate == "" || candidate == primary {
			continue
		}
		return primary, candidate
	}
	return primary, ""
}
