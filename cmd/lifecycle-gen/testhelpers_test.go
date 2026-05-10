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
