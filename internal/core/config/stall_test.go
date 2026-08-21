package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLoadFromParsesStallBlock pins the TOML decode of the [stall] block: the
// threshold duration string round-trips and resolves to the configured value.
func TestLoadFromParsesStallBlock(t *testing.T) {
	dir := t.TempDir()
	body := `
[stall]
threshold = "90s"
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if loaded.Config.Stall.Threshold != "90s" {
		t.Errorf("Threshold = %q, want %q", loaded.Config.Stall.Threshold, "90s")
	}
	if got := loaded.Config.Stall.ThresholdOrDefault(); got != 90*time.Second {
		t.Errorf("ThresholdOrDefault() = %v, want 90s", got)
	}
}

// TestLoadFromMissingStallBlockAppliesDefault pins the documented default: a
// springfield.toml with no [stall] block resolves to DefaultStallThreshold.
func TestLoadFromMissingStallBlockAppliesDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := loaded.Config.Stall.ThresholdOrDefault(); got != DefaultStallThreshold {
		t.Errorf("ThresholdOrDefault() = %v, want default %v", got, DefaultStallThreshold)
	}
}

// TestStallThresholdZeroDisables pins that an explicit "0" disables detection
// (resolves to 0), distinct from an omitted key that defaults.
func TestStallThresholdZeroDisables(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{"explicit zero disables", "0", 0},
		{"negative disables", "-5m", 0},
		{"omitted defaults", "", DefaultStallThreshold},
		{"unparseable defaults", "banana", DefaultStallThreshold},
		{"positive passes through", "45s", 45 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StallConfig{Threshold: tc.raw}.ThresholdOrDefault()
			if got != tc.want {
				t.Errorf("ThresholdOrDefault(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestLoadFromUnknownKeyReturnsInvalidConfigError pins strict-mode decoding for
// the committed config, mirroring the local loader: a typo in the [stall] block
// (`threshhold`) must produce a diagnostic error, not silently leave detection
// at its default.
func TestLoadFromUnknownKeyReturnsInvalidConfigError(t *testing.T) {
	dir := t.TempDir()
	body := `
[stall]
threshold = "5m"
threshhold = "10m"  # typo, should error
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFrom(dir)
	if err == nil {
		t.Fatal("expected error on unknown key, got nil")
	}
	var invalid *InvalidConfigError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected *InvalidConfigError, got %T", err)
	}
	if !strings.Contains(invalid.Reason, "unknown") {
		t.Fatalf("error reason should mention unknown keys: %q", invalid.Reason)
	}
}
