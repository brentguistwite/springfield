package main

import (
	"os"
	"path/filepath"
	"runtime"
)

// conductorTypesPath returns the absolute path to the live conductor types.go
// regardless of the cwd the test is invoked from.
func conductorTypesPath() string {
	_, file, _, _ := runtime.Caller(0)
	// .../cmd/lifecycle-gen/testhelpers_test.go -> repo root
	repoRoot := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(repoRoot, "internal", "features", "conductor", "types.go")
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// transitionSourcePaths returns the live Go source files the lifecycle-gen
// walker scans for `// lifecycle:edge` markers. Update this list when a new
// file becomes a source of plan-status mutations.
func transitionSourcePaths() []string {
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(file), "..", "..")
	rels := []string{
		filepath.Join("cmd", "start.go"),
		filepath.Join("cmd", "recover.go"),
		filepath.Join("internal", "features", "conductor", "recover.go"),
		filepath.Join("internal", "features", "conductor", "planrun", "runner.go"),
		filepath.Join("internal", "core", "runtime", "runner.go"),
	}
	out := make([]string, len(rels))
	for i, r := range rels {
		out[i] = filepath.Join(repoRoot, r)
	}
	return out
}
