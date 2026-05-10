package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAutoBranchEnabledDefault(t *testing.T) {
	var c Config
	if !c.AutoBranchEnabled() {
		t.Fatal("default AutoBranchEnabled must be true (nil pointer)")
	}
}

func TestAutoBranchEnabledExplicitFalse(t *testing.T) {
	f := false
	c := Config{Project: ProjectConfig{AutoBranch: &f}}
	if c.AutoBranchEnabled() {
		t.Fatal("explicit auto_branch=false must disable")
	}
}

func TestAutoBranchEnabledExplicitTrue(t *testing.T) {
	tr := true
	c := Config{Project: ProjectConfig{AutoBranch: &tr}}
	if !c.AutoBranchEnabled() {
		t.Fatal("explicit auto_branch=true must enable")
	}
}

func TestAutoBranchPatternDefault(t *testing.T) {
	var c Config
	if got := c.AutoBranchPatternOrDefault(); got != DefaultAutoBranchPattern {
		t.Fatalf("default pattern got %q want %q", got, DefaultAutoBranchPattern)
	}
}

func TestAutoBranchPatternWhitespaceFallsBackToDefault(t *testing.T) {
	c := Config{Project: ProjectConfig{AutoBranchPattern: "   "}}
	if got := c.AutoBranchPatternOrDefault(); got != DefaultAutoBranchPattern {
		t.Fatalf("whitespace pattern must fall back, got %q", got)
	}
}

func TestAutoBranchPatternCustom(t *testing.T) {
	c := Config{Project: ProjectConfig{AutoBranchPattern: "feat/auto-{id}"}}
	if got := c.AutoBranchPatternOrDefault(); got != "feat/auto-{id}" {
		t.Fatalf("custom pattern got %q", got)
	}
}

func TestLoadAutoBranchFromTOML(t *testing.T) {
	dir := t.TempDir()
	content := `[project]
agent_priority = ["claude"]
auto_branch = false
auto_branch_pattern = "feat/{id}"
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	loaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if loaded.Config.AutoBranchEnabled() {
		t.Fatal("auto_branch=false in toml must disable")
	}
	if got := loaded.Config.AutoBranchPatternOrDefault(); got != "feat/{id}" {
		t.Fatalf("pattern got %q", got)
	}
}

func TestLoadAutoBranchOmittedDefaults(t *testing.T) {
	dir := t.TempDir()
	content := `[project]
agent_priority = ["claude"]
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	loaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if !loaded.Config.AutoBranchEnabled() {
		t.Fatal("omitted auto_branch must default to enabled")
	}
	if got := loaded.Config.AutoBranchPatternOrDefault(); got != DefaultAutoBranchPattern {
		t.Fatalf("default pattern got %q", got)
	}
}
