package conductor

import (
	"errors"
	"os"
	"path/filepath"

	"springfield/internal/storage"
)

// SetupOptions holds guided inputs for conductor config generation.
type SetupOptions struct {
	Tool            string
	FallbackTool    string
	PlansDir        string
	WorktreeBase    string
	MaxRetries      int
	RalphIterations int
	RalphTimeout    int
	Sequential      []string
	Batches         [][]string
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
		PlansDir:        ".conductor/plans",
		WorktreeBase:    ".worktrees",
		MaxRetries:      2,
		RalphIterations: 50,
		RalphTimeout:    3600,
	}
}

// Setup generates .springfield/conductor/config.json from guided inputs.
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
		return SetupResult{
			Created: false,
			Reused:  true,
			Path:    configFile,
		}, nil
	}

	// Generate new config from options
	cfg := &Config{
		PlansDir:        opts.PlansDir,
		WorktreeBase:    opts.WorktreeBase,
		MaxRetries:      opts.MaxRetries,
		RalphIterations: opts.RalphIterations,
		RalphTimeout:    opts.RalphTimeout,
		Tool:            opts.Tool,
		FallbackTool:    opts.FallbackTool,
		Sequential:      opts.Sequential,
		Batches:         opts.Batches,
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
