package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadFromParsesRetroBlock pins the TOML decode of the [retro] block:
// enabled and items_dir round-trip and resolve to the configured values.
func TestLoadFromParsesRetroBlock(t *testing.T) {
	dir := t.TempDir()
	itemsDir := filepath.Join(dir, "vault", "items")
	body := "[retro]\nenabled = true\nitems_dir = \"" + itemsDir + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := loaded.Config.Retro.EnabledOrDefault(); !got {
		t.Errorf("EnabledOrDefault() = %v, want true", got)
	}
	if got := loaded.Config.Retro.ItemsDir; got != itemsDir {
		t.Errorf("ItemsDir = %q, want %q", got, itemsDir)
	}
	if got := loaded.Config.Retro.FilingEnabled(); !got {
		t.Errorf("FilingEnabled() = %v, want true", got)
	}
}

// TestLoadFromMissingRetroBlockAppliesDefaults pins the documented defaults: a
// springfield.toml with no [retro] block leaves the loop enabled and filing
// disabled (empty items_dir).
func TestLoadFromMissingRetroBlockAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := loaded.Config.Retro.EnabledOrDefault(); !got {
		t.Errorf("EnabledOrDefault() = %v, want default true", got)
	}
	if got := loaded.Config.Retro.FilingEnabled(); got {
		t.Errorf("FilingEnabled() = %v, want default false", got)
	}
}

// TestRetroEnabledResolution pins the deliberate asymmetry between an omitted
// key (defaults true) and an explicit enabled = false (turns the loop off).
func TestRetroEnabledResolution(t *testing.T) {
	f := false
	tr := true
	cases := []struct {
		name string
		cfg  RetroConfig
		want bool
	}{
		{"omitted defaults true", RetroConfig{}, true},
		{"explicit false disables", RetroConfig{Enabled: &f}, false},
		{"explicit true enables", RetroConfig{Enabled: &tr}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.EnabledOrDefault(); got != tc.want {
				t.Errorf("EnabledOrDefault() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRetroFilingEnabled pins that only a non-blank items_dir enables filing.
func TestRetroFilingEnabled(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"empty disables", "", false},
		{"blank disables", "   ", false},
		{"set enables", "/abs/vault/items", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (RetroConfig{ItemsDir: tc.dir}).FilingEnabled(); got != tc.want {
				t.Errorf("FilingEnabled(%q) = %v, want %v", tc.dir, got, tc.want)
			}
		})
	}
}

// TestLoadFromRetroUnknownKeyReturnsInvalidConfigError pins strict-mode decoding
// for the [retro] block: a typo (`item_dir`) must produce a diagnostic error,
// not silently leave filing disabled.
func TestLoadFromRetroUnknownKeyReturnsInvalidConfigError(t *testing.T) {
	dir := t.TempDir()
	body := "[retro]\nenabled = true\nitem_dir = \"/tmp/items\"  # typo, should error\n"
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

// TestLoadFromRetroRelativeItemsDirRejected pins that a non-empty, non-absolute
// items_dir fails validation the same way other invalid values do — as an
// *InvalidConfigError from LoadFrom.
func TestLoadFromRetroRelativeItemsDirRejected(t *testing.T) {
	dir := t.TempDir()
	body := "[retro]\nitems_dir = \"vault/items\"\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFrom(dir)
	if err == nil {
		t.Fatal("expected error on relative items_dir, got nil")
	}
	var invalid *InvalidConfigError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected *InvalidConfigError, got %T", err)
	}
	if !strings.Contains(invalid.Reason, "items_dir") {
		t.Fatalf("error reason should mention items_dir: %q", invalid.Reason)
	}
}

// TestLoadFromRetroBlankItemsDirNormalizedToDisabled pins that a whitespace-only
// items_dir normalizes to empty (filing disabled) rather than tripping the
// absolute-path validation — trimming happens before validation.
func TestLoadFromRetroBlankItemsDirNormalizedToDisabled(t *testing.T) {
	dir := t.TempDir()
	body := "[retro]\nitems_dir = \"   \"\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if loaded.Config.Retro.ItemsDir != "" {
		t.Errorf("ItemsDir = %q, want normalized to empty", loaded.Config.Retro.ItemsDir)
	}
	if loaded.Config.Retro.FilingEnabled() {
		t.Error("FilingEnabled() = true, want false for blank items_dir")
	}
}
