package config

import (
	"fmt"
	"os"
)

const FileName = "springfield.toml"

// Load resolves springfield.toml from the current directory upward.
func Load() (Loaded, error) {
	startDir, err := os.Getwd()
	if err != nil {
		return Loaded{}, fmt.Errorf("resolve working directory: %w", err)
	}

	return LoadFrom(startDir)
}

// LoadFrom resolves and loads springfield.toml from startDir upward.
func LoadFrom(startDir string) (Loaded, error) {
	return loadFrom(startDir)
}
