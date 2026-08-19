package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLoadFromParsesSetupBlock pins the TOML decode of the [setup] block from
// springfield.toml: every field round-trips from text into SetupConfig.
func TestLoadFromParsesSetupBlock(t *testing.T) {
	dir := t.TempDir()
	body := `
[setup]
enabled = true
command = "npm install"
timeout = "5m"
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	s := loaded.Config.Setup
	if !s.Enabled {
		t.Errorf("Enabled = false, want true")
	}
	if s.Command != "npm install" {
		t.Errorf("Command = %q, want %q", s.Command, "npm install")
	}
	if got := s.TimeoutOrDefault(); got != 5*time.Minute {
		t.Errorf("TimeoutOrDefault = %v, want 5m", got)
	}
	if !s.ShouldRun() {
		t.Errorf("ShouldRun = false, want true")
	}
}

// TestLoadFromParsesTeardown pins the TOML decode of the optional teardown key
// in the [setup] block and proves ShouldTeardown gates on it, and that the
// block's timeout governs teardown too.
func TestLoadFromParsesTeardown(t *testing.T) {
	dir := t.TempDir()
	body := `
[setup]
enabled = true
command = "npm install"
teardown = "docker compose down"
timeout = "5m"
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	s := loaded.Config.Setup
	if s.Teardown != "docker compose down" {
		t.Errorf("Teardown = %q, want %q", s.Teardown, "docker compose down")
	}
	if !s.ShouldTeardown() {
		t.Errorf("ShouldTeardown = false, want true")
	}
	if got := s.TimeoutOrDefault(); got != 5*time.Minute {
		t.Errorf("TimeoutOrDefault = %v, want 5m (shared with teardown)", got)
	}
}

// TestShouldTeardownIndependentOfCommand proves teardown can be configured
// without a setup command, and stays inert when disabled or blank.
func TestShouldTeardownIndependentOfCommand(t *testing.T) {
	// Teardown without a setup command still runs when enabled.
	if s := (SetupConfig{Enabled: true, Teardown: "docker compose down"}); !s.ShouldTeardown() {
		t.Errorf("ShouldTeardown = false for teardown-only config, want true")
	}
	// Disabled block never tears down even with a command.
	if s := (SetupConfig{Enabled: false, Teardown: "docker compose down"}); s.ShouldTeardown() {
		t.Errorf("ShouldTeardown = true while disabled, want false")
	}
	// Whitespace-only teardown stays inert.
	if s := (SetupConfig{Enabled: true, Teardown: "   "}); s.ShouldTeardown() {
		t.Errorf("ShouldTeardown = true for whitespace teardown, want false")
	}
	// Zero value is off.
	if s := (SetupConfig{}); s.ShouldTeardown() {
		t.Errorf("zero-value ShouldTeardown = true, want false")
	}
}

// TestSetupConfigZeroValueIsOptIn proves an absent [setup] block leaves setup
// off: ShouldRun is false so the runner keeps its prior create-and-dispatch
// behavior.
func TestSetupConfigZeroValueIsOptIn(t *testing.T) {
	var s SetupConfig
	if s.ShouldRun() {
		t.Fatalf("zero-value SetupConfig.ShouldRun = true, want false")
	}
	if got := s.TimeoutOrDefault(); got != DefaultSetupTimeout {
		t.Errorf("TimeoutOrDefault = %v, want %v", got, DefaultSetupTimeout)
	}
}

// TestSetupConfigEnabledWithoutCommandStaysInert proves the command check is
// what preserves the opt-in guarantee: Enabled with no command does not run.
func TestSetupConfigEnabledWithoutCommandStaysInert(t *testing.T) {
	s := SetupConfig{Enabled: true, Command: "   "}
	if s.ShouldRun() {
		t.Fatalf("ShouldRun = true for whitespace command, want false")
	}
}

// TestSetupConfigInvalidTimeoutFallsBack proves an unparseable timeout resolves
// to the default rather than a zero/negative ceiling.
func TestSetupConfigInvalidTimeoutFallsBack(t *testing.T) {
	s := SetupConfig{Timeout: "not-a-duration"}
	if got := s.TimeoutOrDefault(); got != DefaultSetupTimeout {
		t.Errorf("TimeoutOrDefault = %v, want %v", got, DefaultSetupTimeout)
	}
}
