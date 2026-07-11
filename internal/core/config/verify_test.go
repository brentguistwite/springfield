package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"springfield/internal/features/prd"
)

// TestLoadFromParsesVerifyBlock pins the TOML decode of the [verify] block from
// springfield.toml: every field round-trips from text into VerifyConfig.
func TestLoadFromParsesVerifyBlock(t *testing.T) {
	dir := t.TempDir()
	body := `
[verify]
enabled = true
command = "go test ./..."
timeout = "5m"
max_verify_iterations = 2
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	v := loaded.Config.Verify
	if !v.Enabled {
		t.Errorf("Enabled = false, want true")
	}
	if v.Command != "go test ./..." {
		t.Errorf("Command = %q, want %q", v.Command, "go test ./...")
	}
	if v.Timeout != "5m" {
		t.Errorf("Timeout = %q, want %q", v.Timeout, "5m")
	}
	if got := v.TimeoutOrDefault(); got != 5*time.Minute {
		t.Errorf("TimeoutOrDefault() = %v, want 5m", got)
	}
	if v.MaxVerifyIterations != 2 {
		t.Errorf("MaxVerifyIterations = %d, want 2", v.MaxVerifyIterations)
	}
}

// TestLoadFromMissingVerifyBlockLeavesGateDisabled pins the opt-in default: a
// springfield.toml with no [verify] block decodes to the zero VerifyConfig,
// which leaves the gate off (marker-only completion unchanged).
func TestLoadFromMissingVerifyBlockLeavesGateDisabled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if loaded.Config.Verify.Enabled {
		t.Fatalf("missing [verify] should leave gate disabled, got Enabled=true")
	}
	if VerifyEnabledForPlan(loaded.Config.Verify, nil) {
		t.Fatalf("VerifyEnabledForPlan on zero config should be false")
	}
}

func TestVerifyConfigTimeoutOrDefault(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"unset defaults to 20m", "", DefaultVerifyTimeout},
		{"unparseable defaults to 20m", "not-a-duration", DefaultVerifyTimeout},
		{"zero defaults to 20m", "0s", DefaultVerifyTimeout},
		{"negative defaults to 20m", "-3m", DefaultVerifyTimeout},
		{"explicit kept", "90s", 90 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := VerifyConfig{Timeout: tc.in}
			if got := v.TimeoutOrDefault(); got != tc.want {
				t.Fatalf("TimeoutOrDefault() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVerifyConfigMaxVerifyIterationsOrDefault(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"unset defaults to 3", 0, 3},
		{"negative defaults to 3", -2, 3},
		{"explicit positive kept", 5, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := VerifyConfig{MaxVerifyIterations: tc.in}
			if got := v.MaxVerifyIterationsOrDefault(); got != tc.want {
				t.Fatalf("MaxVerifyIterationsOrDefault() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDefaultsVerify(t *testing.T) {
	if DefaultMaxVerifyIterations != 3 {
		t.Fatalf("DefaultMaxVerifyIterations = %d, want 3", DefaultMaxVerifyIterations)
	}
	if DefaultVerifyTimeout != 20*time.Minute {
		t.Fatalf("DefaultVerifyTimeout = %v, want 20m", DefaultVerifyTimeout)
	}
}

// TestVerifyEnabledForPlanPrecedence pins per-plan-wins in both directions,
// including the acceptance-criteria case: enabled:false forces the gate off for
// one plan even when the project globally enables it.
func TestVerifyEnabledForPlanPrecedence(t *testing.T) {
	cmd := "go test ./..."
	cases := []struct {
		name        string
		global      VerifyConfig
		perPlan     *prd.VerifyOverride
		wantEnabled bool
	}{
		{"omitted override → global off", VerifyConfig{Enabled: false, Command: cmd}, nil, false},
		{"omitted override → global on", VerifyConfig{Enabled: true, Command: cmd}, nil, true},
		{"per-plan true overrides global off", VerifyConfig{Enabled: false, Command: cmd}, &prd.VerifyOverride{Enabled: boolPtr(true)}, true},
		{"per-plan false overrides global on", VerifyConfig{Enabled: true, Command: cmd}, &prd.VerifyOverride{Enabled: boolPtr(false)}, false},
		{"enabled but no command stays inert", VerifyConfig{Enabled: true, Command: ""}, nil, false},
		{"per-plan command supplies missing global command", VerifyConfig{Enabled: true, Command: ""}, &prd.VerifyOverride{Command: cmd}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := VerifyEnabledForPlan(tc.global, tc.perPlan); got != tc.wantEnabled {
				t.Fatalf("VerifyEnabledForPlan = %v, want %v", got, tc.wantEnabled)
			}
		})
	}
}

// TestResolveVerifyPerPlanWins pins the full resolution: per-plan command
// replaces global, per-plan enabled flips the toggle, and timeout/max-iterations
// resolve from the global block's defaults (they are not per-plan overridable).
func TestResolveVerifyPerPlanWins(t *testing.T) {
	global := VerifyConfig{
		Enabled:             true,
		Command:             "go test ./...",
		Timeout:             "5m",
		MaxVerifyIterations: 4,
	}

	// nil override inherits everything.
	got := ResolveVerify(global, nil)
	if !got.Enabled || got.Command != "go test ./..." || got.Timeout != 5*time.Minute || got.MaxIterations != 4 {
		t.Fatalf("nil override resolve mismatch: %+v", got)
	}

	// Per-plan command wins; enabled flipped off; timeout/max still global.
	got = ResolveVerify(global, &prd.VerifyOverride{
		Command: "make check",
		Enabled: boolPtr(false),
	})
	if got.Enabled {
		t.Errorf("Enabled = true, want false (per-plan override)")
	}
	if got.Command != "make check" {
		t.Errorf("Command = %q, want %q (per-plan wins)", got.Command, "make check")
	}
	if got.Timeout != 5*time.Minute {
		t.Errorf("Timeout = %v, want 5m (global, not overridable)", got.Timeout)
	}
	if got.MaxIterations != 4 {
		t.Errorf("MaxIterations = %d, want 4 (global, not overridable)", got.MaxIterations)
	}

	// Empty per-plan command inherits the global command (does not blank it).
	got = ResolveVerify(global, &prd.VerifyOverride{Command: "   "})
	if got.Command != "go test ./..." {
		t.Errorf("Command = %q, want global %q for whitespace override", got.Command, "go test ./...")
	}
}
