package conductor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"springfield/internal/storage"
)

const (
	LocalPlansDir   = ".springfield/conductor/plans"
	TrackedPlansDir = ".conductor/plans"
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
	UpdateGitignore bool
}

// SetupResult describes what happened during setup.
type SetupResult struct {
	Created          bool
	Reused           bool
	Path             string
	GitignoreUpdated bool
}

// SetupDefaults returns reasonable defaults for conductor config generation.
func SetupDefaults() SetupOptions {
	return SetupOptions{
		PlansDir:        LocalPlansDir,
		WorktreeBase:    ".worktrees",
		MaxRetries:      2,
		RalphIterations: 50,
		RalphTimeout:    3600,
		Sequential:      []string{},
		Batches:         [][]string{},
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
	sequential := opts.Sequential
	if sequential == nil {
		sequential = []string{}
	}

	batches := opts.Batches
	if batches == nil {
		batches = [][]string{}
	}

	cfg := &Config{
		PlansDir:        opts.PlansDir,
		WorktreeBase:    opts.WorktreeBase,
		MaxRetries:      opts.MaxRetries,
		RalphIterations: opts.RalphIterations,
		RalphTimeout:    opts.RalphTimeout,
		Tool:            opts.Tool,
		FallbackTool:    opts.FallbackTool,
		Sequential:      sequential,
		Batches:         batches,
	}

	if err := rt.WriteJSON(configPath, cfg); err != nil {
		return SetupResult{}, err
	}

	gitignoreUpdated := false
	if opts.UpdateGitignore && cfg.PlansDir == TrackedPlansDir {
		updated, err := ensureTrackedPlansGitignore(rootDir)
		if err != nil {
			return SetupResult{}, err
		}
		gitignoreUpdated = updated
	}

	return SetupResult{
		Created:          true,
		Reused:           false,
		Path:             configFile,
		GitignoreUpdated: gitignoreUpdated,
	}, nil
}

// UpdateResult describes what happened during a config update.
type UpdateResult struct {
	Updated          bool
	Path             string
	GitignoreUpdated bool
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
		return UpdateResult{}, errors.New("no existing conductor config to update; use Setup for first-run")
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
		PlansDir:        opts.PlansDir,
		WorktreeBase:    opts.WorktreeBase,
		MaxRetries:      opts.MaxRetries,
		RalphIterations: opts.RalphIterations,
		RalphTimeout:    opts.RalphTimeout,
		Tool:            opts.Tool,
		FallbackTool:    opts.FallbackTool,
		Sequential:      sequential,
		Batches:         batches,
	}

	if err := rt.WriteJSON(configPath, cfg); err != nil {
		return UpdateResult{}, err
	}

	gitignoreUpdated := false
	if opts.UpdateGitignore && cfg.PlansDir == TrackedPlansDir {
		updated, err := ensureTrackedPlansGitignore(rootDir)
		if err != nil {
			return UpdateResult{}, err
		}
		gitignoreUpdated = updated
	}

	return UpdateResult{
		Updated:          true,
		Path:             configFile,
		GitignoreUpdated: gitignoreUpdated,
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

func TrackedPlansGitignoreSnippet() string {
	return ".conductor/*\n!.conductor/plans/\n"
}

func ensureTrackedPlansGitignore(rootDir string) (bool, error) {
	path := filepath.Join(rootDir, ".gitignore")

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	content := string(data)
	required := []string{".conductor/*", "!.conductor/plans/"}
	missing := make([]string, 0, len(required))
	for _, line := range required {
		if !strings.Contains(content, line) {
			missing = append(missing, line)
		}
	}
	if len(missing) == 0 {
		return false, nil
	}

	var builder strings.Builder
	trimmed := strings.TrimRight(content, "\n")
	if trimmed != "" {
		builder.WriteString(trimmed)
		builder.WriteString("\n")
	}
	for _, line := range missing {
		builder.WriteString(line)
		builder.WriteString("\n")
	}

	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		return false, err
	}

	return true, nil
}
