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
