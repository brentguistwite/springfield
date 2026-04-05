package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/core/config"
)

func TestInitCreatesConfigAndRuntimeDir(t *testing.T) {
	dir := t.TempDir()

	result, err := config.Init(dir)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Config file should exist and be loadable
	configPath := filepath.Join(dir, config.FileName)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	// Runtime dir should exist
	runtimeDir := filepath.Join(dir, ".springfield")
	info, err := os.Stat(runtimeDir)
	if err != nil {
		t.Fatalf(".springfield dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal(".springfield should be a directory")
	}

	// Result should report both created
	if !result.ConfigCreated {
		t.Error("expected ConfigCreated=true")
	}
	if !result.RuntimeDirCreated {
		t.Error("expected RuntimeDirCreated=true")
	}

	// Created config should be loadable with valid defaults
	loaded, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatalf("created config should be loadable: %v", err)
	}
	if loaded.Config.Project.DefaultAgent == "" {
		t.Error("default agent should be non-empty")
	}
}

func TestInitIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	// First init
	config.Init(dir)

	// Modify the config to prove re-run doesn't overwrite
	configPath := filepath.Join(dir, config.FileName)
	custom := []byte("[project]\ndefault_agent = \"custom-agent\"\n")
	if err := os.WriteFile(configPath, custom, 0644); err != nil {
		t.Fatal(err)
	}

	// Add a file inside .springfield to prove it's preserved
	marker := filepath.Join(dir, ".springfield", "marker.json")
	os.WriteFile(marker, []byte(`{}`), 0644)

	// Re-run init
	result, err := config.Init(dir)
	if err != nil {
		t.Fatalf("re-run Init failed: %v", err)
	}

	// Should report nothing created
	if result.ConfigCreated {
		t.Error("should not recreate existing config")
	}
	if result.RuntimeDirCreated {
		t.Error("should not recreate existing runtime dir")
	}

	// Config content should be preserved
	loaded, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load after re-init: %v", err)
	}
	if loaded.Config.Project.DefaultAgent != "custom-agent" {
		t.Errorf("config was overwritten: got %q", loaded.Config.Project.DefaultAgent)
	}

	// Marker file should still exist
	if _, err := os.Stat(marker); err != nil {
		t.Error("runtime dir contents were destroyed")
	}
}
