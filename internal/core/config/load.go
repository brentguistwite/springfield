package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// MissingConfigError reports that no springfield.toml exists in the current
// project tree.
type MissingConfigError struct {
	StartDir string
}

func (e *MissingConfigError) Error() string {
	return fmt.Sprintf(
		"missing %s from %s upward; run \"springfield init\" in the repo root or create %s there",
		FileName,
		e.StartDir,
		FileName,
	)
}

// InvalidConfigError reports a springfield.toml parse or validation error.
type InvalidConfigError struct {
	Path   string
	Reason string
}

func (e *InvalidConfigError) Error() string {
	return fmt.Sprintf("invalid %s at %s: %s", FileName, e.Path, e.Reason)
}

func loadFrom(startDir string) (Loaded, error) {
	rootDir, configPath, err := findConfig(startDir)
	if err != nil {
		return Loaded{}, err
	}

	var cfg Config
	if _, err := toml.DecodeFile(configPath, &cfg); err != nil {
		return Loaded{}, &InvalidConfigError{
			Path:   configPath,
			Reason: err.Error(),
		}
	}
	normalize(&cfg)

	if err := validate(cfg); err != nil {
		return Loaded{}, &InvalidConfigError{
			Path:   configPath,
			Reason: err.Error(),
		}
	}

	return Loaded{
		RootDir: rootDir,
		Path:    configPath,
		Config:  cfg,
	}, nil
}

func findConfig(startDir string) (string, string, error) {
	absStartDir, err := filepath.Abs(startDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve config search path %q: %w", startDir, err)
	}

	current := absStartDir
	for {
		configPath := filepath.Join(current, FileName)
		info, err := os.Stat(configPath)
		switch {
		case err == nil && !info.IsDir():
			return current, configPath, nil
		case err == nil && info.IsDir():
			return "", "", &InvalidConfigError{
				Path:   configPath,
				Reason: "expected a file, found a directory",
			}
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return "", "", fmt.Errorf("stat %s: %w", configPath, err)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", "", &MissingConfigError{StartDir: absStartDir}
		}

		current = parent
	}
}

func validate(cfg Config) error {
	if strings.TrimSpace(cfg.Project.DefaultAgent) == "" {
		return fmt.Errorf("project.default_agent must be set")
	}

	for planID, plan := range cfg.Plans {
		if strings.TrimSpace(plan.Agent) == "" {
			return fmt.Errorf("plans.%s.agent must be set when declared", planID)
		}
	}

	if err := validateEnum(
		"agents.claude.permission_mode",
		cfg.Agents.Claude.PermissionMode,
		[]string{"acceptEdits", "auto", "bypassPermissions", "default", "dontAsk", "plan"},
	); err != nil {
		return err
	}

	if err := validateEnum(
		"agents.codex.sandbox_mode",
		cfg.Agents.Codex.SandboxMode,
		[]string{"read-only", "workspace-write", "danger-full-access"},
	); err != nil {
		return err
	}

	if err := validateEnum(
		"agents.codex.approval_policy",
		cfg.Agents.Codex.ApprovalPolicy,
		[]string{"untrusted", "on-request", "never"},
	); err != nil {
		return err
	}

	return nil
}

func validateEnum(key, value string, allowed []string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if slices.Contains(allowed, value) {
		return nil
	}
	return fmt.Errorf("%s must be one of %s", key, strings.Join(allowed, ", "))
}

func normalize(cfg *Config) {
	cfg.Agents.Claude.PermissionMode = strings.TrimSpace(cfg.Agents.Claude.PermissionMode)
	cfg.Agents.Codex.SandboxMode = strings.TrimSpace(cfg.Agents.Codex.SandboxMode)
	cfg.Agents.Codex.ApprovalPolicy = strings.TrimSpace(cfg.Agents.Codex.ApprovalPolicy)
}
