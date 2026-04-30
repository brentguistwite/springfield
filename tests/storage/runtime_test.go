package storage_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/config"
	"springfield/internal/storage"
)

func writeConfigFile(t *testing.T, root string, body string) string {
	t.Helper()

	path := filepath.Join(root, config.FileName)
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	return path
}

func TestResolveFromUsesConfigRootForRuntimePaths(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "plans", "release")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	writeConfigFile(t, root, `
[project]
agent_priority = ["claude"]
`)

	runtime, err := storage.ResolveFrom(nested)
	if err != nil {
		t.Fatalf("resolve runtime: %v", err)
	}

	if runtime.RootDir != root {
		t.Fatalf("expected root dir %q, got %q", root, runtime.RootDir)
	}

	wantDir := filepath.Join(root, storage.DirName)
	if runtime.Dir != wantDir {
		t.Fatalf("expected runtime dir %q, got %q", wantDir, runtime.Dir)
	}

	path, err := runtime.Path("runs", "latest.json")
	if err != nil {
		t.Fatalf("resolve runtime path: %v", err)
	}

	wantPath := filepath.Join(wantDir, "runs", "latest.json")
	if path != wantPath {
		t.Fatalf("expected runtime path %q, got %q", wantPath, path)
	}
}

func TestRuntimeEnsureCreatesDotSpringfieldDir(t *testing.T) {
	root := t.TempDir()

	runtime, err := storage.FromRoot(root)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}

	if _, err := os.Stat(runtime.Dir); !os.IsNotExist(err) {
		t.Fatalf("expected runtime dir to start missing, got err=%v", err)
	}

	if err := runtime.Ensure(); err != nil {
		t.Fatalf("ensure runtime dir: %v", err)
	}

	info, err := os.Stat(runtime.Dir)
	if err != nil {
		t.Fatalf("stat runtime dir: %v", err)
	}

	if !info.IsDir() {
		t.Fatalf("expected runtime path %q to be a directory", runtime.Dir)
	}
}

func TestRuntimeJSONHelpersCreateParentsAndRoundTrip(t *testing.T) {
	root := t.TempDir()

	runtime, err := storage.FromRoot(root)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}

	type state struct {
		Plan  string `json:"plan"`
		Agent string `json:"agent"`
	}

	want := state{
		Plan:  "release",
		Agent: "claude",
	}

	if err := runtime.WriteJSON("plans/release/state.json", want); err != nil {
		t.Fatalf("write json: %v", err)
	}

	path, err := runtime.Path("plans", "release", "state.json")
	if err != nil {
		t.Fatalf("resolve json path: %v", err)
	}

	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("expected parent dirs to exist: %v", err)
	}

	var got state
	if err := runtime.ReadJSON("plans/release/state.json", &got); err != nil {
		t.Fatalf("read json: %v", err)
	}

	if got != want {
		t.Fatalf("expected round-trip state %+v, got %+v", want, got)
	}
}
