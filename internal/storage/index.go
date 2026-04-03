package storage

import (
	"fmt"
	"os"

	"springfield/internal/core/config"
)

const DirName = ".springfield"

// Resolve resolves runtime storage from the current directory upward.
func Resolve() (Runtime, error) {
	startDir, err := os.Getwd()
	if err != nil {
		return Runtime{}, fmt.Errorf("resolve working directory: %w", err)
	}

	return ResolveFrom(startDir)
}

// ResolveFrom resolves runtime storage from a start directory by locating the
// repo-root springfield.toml.
func ResolveFrom(startDir string) (Runtime, error) {
	loaded, err := config.LoadFrom(startDir)
	if err != nil {
		return Runtime{}, err
	}

	return FromRoot(loaded.RootDir)
}

// FromRoot builds runtime storage paths from an explicit project root.
func FromRoot(rootDir string) (Runtime, error) {
	return newRuntime(rootDir)
}
