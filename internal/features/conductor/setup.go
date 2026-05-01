package conductor

import (
	"errors"
	"os"
	"path/filepath"

	"springfield/internal/storage"
)

const (
	LocalPlansDir   = ".springfield/execution/plans"
	TrackedPlansDir = "springfield/plans"
)

// SetupOptions holds guided inputs for conductor config generation.
type SetupOptions struct {
	Tool                       string
	PlansDir                   string
	WorktreeBase               string
	MaxRetries                 int
	SingleWorkstreamIterations int
	SingleWorkstreamTimeout    int
	Sequential                 []string
	Batches                    [][]string
}

// SetupResult describes what happened during setup.
type SetupResult struct {
	Created bool
	Reused  bool
	Path    string
}

// SetupDefaults returns reasonable defaults for conductor config generation.
func SetupDefaults() SetupOptions {
	return SetupOptions{
		PlansDir:                   LocalPlansDir,
		WorktreeBase:               ".worktrees",
		MaxRetries:                 2,
		SingleWorkstreamIterations: 50,
		SingleWorkstreamTimeout:    3600,
		Sequential:                 []string{},
		Batches:                    [][]string{},
	}
}

// Setup generates Springfield-managed execution config from guided inputs.
// If valid config already exists, it is preserved and reused.
func Setup(rootDir string, opts SetupOptions) (SetupResult, error) {
	rt, err := storage.FromRoot(rootDir)
	if err != nil {
		return SetupResult{}, err
	}

	// Verify project is initialized
	tomlPath := filepath.Join(rootDir, "springfield.toml")
	if _, err := os.Stat(tomlPath); err != nil {
		return SetupResult{}, errors.New("project not initialized: run springfield init first")
	}

	configFile, _ := rt.Path(configPath)

	// Check for existing valid config
	var existing Config
	if err := rt.ReadJSON(configPath, &existing); err == nil {
		existingFile, _ := rt.Path(configPath)
		return SetupResult{
			Created: false,
			Reused:  true,
			Path:    existingFile,
		}, nil
	}

	// Generate new config from options
	sequential := opts.Sequential
	if sequential == nil {
		sequential = []string{}
	}

	batches := opts.Batches
	if batches == nil {
		batches = [][]string{}
	}

	cfg := &Config{
		PlansDir:                   opts.PlansDir,
		WorktreeBase:               opts.WorktreeBase,
		MaxRetries:                 opts.MaxRetries,
		SingleWorkstreamIterations: opts.SingleWorkstreamIterations,
		SingleWorkstreamTimeout:    opts.SingleWorkstreamTimeout,
		Tool:                       opts.Tool,
		Sequential:                 sequential,
		Batches:                    batches,
	}

	if err := rt.WriteJSON(configPath, cfg); err != nil {
		return SetupResult{}, err
	}

	return SetupResult{
		Created: true,
		Reused:  false,
		Path:    configFile,
	}, nil
}

// UpdateResult describes what happened during a config update.
type UpdateResult struct {
	Updated bool
	Path    string
}

// UpdateConfig overwrites an existing conductor config with new values.
// Returns an error if no config exists yet (use Setup for first-run).
func UpdateConfig(rootDir string, opts SetupOptions) (UpdateResult, error) {
	rt, err := storage.FromRoot(rootDir)
	if err != nil {
		return UpdateResult{}, err
	}

	configFile, _ := rt.Path(configPath)

	// Verify existing config exists
	var existing Config
	if err := rt.ReadJSON(configPath, &existing); err != nil {
		return UpdateResult{}, errors.New("no existing Springfield execution config to update; use Setup for first-run")
	}

	sequential := opts.Sequential
	if sequential == nil {
		sequential = []string{}
	}
	batches := opts.Batches
	if batches == nil {
		batches = [][]string{}
	}

	cfg := &Config{
		PlansDir:                   opts.PlansDir,
		WorktreeBase:               opts.WorktreeBase,
		MaxRetries:                 opts.MaxRetries,
		SingleWorkstreamIterations: opts.SingleWorkstreamIterations,
		SingleWorkstreamTimeout:    opts.SingleWorkstreamTimeout,
		Tool:                       opts.Tool,
		Sequential:                 sequential,
		Batches:                    batches,
	}

	if err := rt.WriteJSON(configPath, cfg); err != nil {
		return UpdateResult{}, err
	}

	return UpdateResult{
		Updated: true,
		Path:    configFile,
	}, nil
}

// IsReady checks if conductor config exists and is loadable.
func IsReady(rootDir string) (bool, error) {
	rt, err := storage.FromRoot(rootDir)
	if err != nil {
		return false, err
	}

	var cfg Config
	if err := rt.ReadJSON(configPath, &cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, nil
	}

	return true, nil
}
