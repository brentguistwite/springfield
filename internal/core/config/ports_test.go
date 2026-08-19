package config

import (
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/features/portblock"
)

// TestLoadFromParsesPortsBlock pins the TOML decode of the [ports] block.
func TestLoadFromParsesPortsBlock(t *testing.T) {
	dir := t.TempDir()
	body := `
[ports]
base = 50000
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := loaded.Config.Ports.BaseOrDefault(); got != 50000 {
		t.Fatalf("BaseOrDefault = %d, want 50000", got)
	}
}

// TestPortsBaseDefaults proves an omitted or non-positive base falls back to
// the package default rather than 0 (which would put slice 1 at port 0).
func TestPortsBaseDefaults(t *testing.T) {
	for _, base := range []int{0, -1} {
		if got := (PortsConfig{Base: base}).BaseOrDefault(); got != portblock.DefaultBase {
			t.Fatalf("PortsConfig{Base: %d}.BaseOrDefault() = %d, want %d", base, got, portblock.DefaultBase)
		}
	}
}
