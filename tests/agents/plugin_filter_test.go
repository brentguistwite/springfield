package agents_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
)

// writeSettingsJSON writes a minimal ~/.claude/settings.json under homeDir.
func writeSettingsJSON(t *testing.T, homeDir string, enabledPlugins map[string]bool) {
	t.Helper()
	claudeDir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", claudeDir, err)
	}
	data, err := json.Marshal(map[string]any{"enabledPlugins": enabledPlugins})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
}

// settingsEnabledPlugins parses the enabledPlugins from --settings JSON in cmd args.
func settingsEnabledPlugins(t *testing.T, args []string) map[string]bool {
	t.Helper()
	jsonVal := extractSettingsJSON(t, args)
	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonVal), &raw); err != nil {
		t.Fatalf("parse --settings JSON %q: %v", jsonVal, err)
	}
	pluginsRaw, ok := raw["enabledPlugins"]
	if !ok {
		return nil
	}
	pluginsMap, ok := pluginsRaw.(map[string]any)
	if !ok {
		t.Fatalf("enabledPlugins is not a map: %T", pluginsRaw)
	}
	result := make(map[string]bool, len(pluginsMap))
	for k, v := range pluginsMap {
		b, ok := v.(bool)
		if !ok {
			t.Fatalf("enabledPlugins[%q] is not bool: %T", k, v)
		}
		result[k] = b
	}
	return result
}

// TestClaudeAdapterDisablesSpringfieldAndSuperpowersPlugins verifies that when
// both springfield@brentguistwite and superpowers@claude-plugins-official are
// present in the user's settings.json, the adapter disables both in the
// subagent's --settings JSON.
func TestClaudeAdapterDisablesSpringfieldAndSuperpowersPlugins(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeSettingsJSON(t, homeDir, map[string]bool{
		"springfield@brentguistwite":       true,
		"superpowers@claude-plugins-official": true,
	})

	var warnBuf bytes.Buffer
	a := claude.NewWithOptions(nil, claude.Options{WarnWriter: &warnBuf})
	cmd := a.(agents.Commander).Command(agents.CommandInput{Prompt: "do work", WorkDir: "/tmp"})

	plugins := settingsEnabledPlugins(t, cmd.Args)
	if plugins == nil {
		t.Fatal("expected enabledPlugins in --settings JSON")
	}
	if got := plugins["springfield@brentguistwite"]; got != false {
		t.Errorf("springfield@brentguistwite = %v, want false", got)
	}
	if got := plugins["superpowers@claude-plugins-official"]; got != false {
		t.Errorf("superpowers@claude-plugins-official = %v, want false", got)
	}
}

// TestClaudeAdapterMatchesForkedMarketplaceIds verifies that the adapter
// matches on the actual installed plugin ID (e.g. springfield@foo) rather than
// hardcoded canonical slugs.
func TestClaudeAdapterMatchesForkedMarketplaceIds(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeSettingsJSON(t, homeDir, map[string]bool{
		"springfield@foo": true,
		"superpowers@bar": true,
		"other@baz":       true,
	})

	var warnBuf bytes.Buffer
	a := claude.NewWithOptions(nil, claude.Options{WarnWriter: &warnBuf})
	cmd := a.(agents.Commander).Command(agents.CommandInput{Prompt: "do work", WorkDir: "/tmp"})

	plugins := settingsEnabledPlugins(t, cmd.Args)
	if plugins == nil {
		t.Fatal("expected enabledPlugins in --settings JSON")
	}
	if got := plugins["springfield@foo"]; got != false {
		t.Errorf("springfield@foo = %v, want false", got)
	}
	if got := plugins["superpowers@bar"]; got != false {
		t.Errorf("superpowers@bar = %v, want false", got)
	}
	if _, present := plugins["other@baz"]; present {
		t.Errorf("other@baz should not be in enabledPlugins, got %v", plugins)
	}
}

// TestClaudeAdapterSkipsDisableWhenPluginAbsent verifies that when no
// springfield@* or superpowers@* plugins are installed, the adapter emits no
// enabledPlugins block (no-op, avoids polluting settings for unrelated setups).
func TestClaudeAdapterSkipsDisableWhenPluginAbsent(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	// settings readable but only unrelated plugins
	writeSettingsJSON(t, homeDir, map[string]bool{
		"other@baz": true,
	})

	var warnBuf bytes.Buffer
	a := claude.NewWithOptions(nil, claude.Options{WarnWriter: &warnBuf})
	cmd := a.(agents.Commander).Command(agents.CommandInput{Prompt: "do work", WorkDir: "/tmp"})

	plugins := settingsEnabledPlugins(t, cmd.Args)
	if len(plugins) != 0 {
		t.Errorf("expected no enabledPlugins entries, got %v", plugins)
	}
}

// TestClaudeAdapterFallsBackToDefaultsWithWarningOnUnreadableSettings verifies
// that when ~/.claude/settings.json is missing, the adapter:
//  1. writes a warning to warnBuf containing "springfield: cannot read ~/.claude/settings.json"
//  2. injects hardcoded default disables for springfield@brentguistwite and
//     superpowers@claude-plugins-official
func TestClaudeAdapterFallsBackToDefaultsWithWarningOnUnreadableSettings(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	// No .claude/settings.json written — settings unreadable

	var warnBuf bytes.Buffer
	a := claude.NewWithOptions(nil, claude.Options{WarnWriter: &warnBuf})
	cmd := a.(agents.Commander).Command(agents.CommandInput{Prompt: "do work", WorkDir: "/tmp"})

	warning := warnBuf.String()
	if warning == "" {
		t.Fatal("expected warning in warnBuf, got empty")
	}
	if want := "springfield: cannot read ~/.claude/settings.json"; !containsStr(warning, want) {
		t.Errorf("warning %q does not contain %q", warning, want)
	}

	plugins := settingsEnabledPlugins(t, cmd.Args)
	if plugins == nil {
		t.Fatal("expected enabledPlugins in --settings JSON (fallback defaults)")
	}
	if got := plugins["springfield@brentguistwite"]; got != false {
		t.Errorf("springfield@brentguistwite (default) = %v, want false", got)
	}
	if got := plugins["superpowers@claude-plugins-official"]; got != false {
		t.Errorf("superpowers@claude-plugins-official (default) = %v, want false", got)
	}
}

// TestClaudeAdapterReadsSettingsAtCommandTimeNotNewTime verifies that the
// adapter reads settings at Command() invocation, not at New() time. This
// matters when users install/remove plugins during a long-running session.
func TestClaudeAdapterReadsSettingsAtCommandTimeNotNewTime(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	// Initially: no springfield plugin installed
	writeSettingsJSON(t, homeDir, map[string]bool{
		"other@baz": true,
	})

	var warnBuf bytes.Buffer
	a := claude.NewWithOptions(nil, claude.Options{WarnWriter: &warnBuf})

	// Mutate settings AFTER New() but BEFORE Command()
	writeSettingsJSON(t, homeDir, map[string]bool{
		"springfield@brentguistwite": true,
		"other@baz":                  true,
	})

	cmd := a.(agents.Commander).Command(agents.CommandInput{Prompt: "do work", WorkDir: "/tmp"})

	plugins := settingsEnabledPlugins(t, cmd.Args)
	if plugins == nil {
		t.Fatal("expected enabledPlugins in --settings JSON (post-mutation)")
	}
	if got := plugins["springfield@brentguistwite"]; got != false {
		t.Errorf("springfield@brentguistwite = %v, want false (settings should be read at Command time)", got)
	}
}

// TestClaudeAdapterPreservesExistingHookSettings verifies that the hook-guard
// PreToolUse hook is still present alongside the new enabledPlugins block.
func TestClaudeAdapterPreservesExistingHookSettings(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeSettingsJSON(t, homeDir, map[string]bool{
		"springfield@brentguistwite": true,
	})

	var warnBuf bytes.Buffer
	a := claude.NewWithOptions(nil, claude.Options{WarnWriter: &warnBuf})
	cmd := a.(agents.Commander).Command(agents.CommandInput{Prompt: "do work", WorkDir: "/tmp"})

	jsonVal := extractSettingsJSON(t, cmd.Args)
	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonVal), &raw); err != nil {
		t.Fatalf("parse --settings JSON: %v", err)
	}
	if _, ok := raw["hooks"]; !ok {
		t.Error("hooks key missing from --settings JSON")
	}
	if _, ok := raw["enabledPlugins"]; !ok {
		t.Error("enabledPlugins key missing from --settings JSON")
	}
}

// TestClaudeAdapterKeepsUnrelatedPluginsAlone verifies that well-known useful
// plugins (atlassian, context7, codex, caveman, example-skills) are never
// added to the disable list.
func TestClaudeAdapterKeepsUnrelatedPluginsAlone(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	writeSettingsJSON(t, homeDir, map[string]bool{
		"atlassian@atlassian":          true,
		"context7@upstash":             true,
		"codex@openai":                 true,
		"caveman@example":              true,
		"example-skills@example":       true,
		"springfield@brentguistwite":   true,
	})

	var warnBuf bytes.Buffer
	a := claude.NewWithOptions(nil, claude.Options{WarnWriter: &warnBuf})
	cmd := a.(agents.Commander).Command(agents.CommandInput{Prompt: "do work", WorkDir: "/tmp"})

	plugins := settingsEnabledPlugins(t, cmd.Args)
	for _, id := range []string{"atlassian@atlassian", "context7@upstash", "codex@openai", "caveman@example", "example-skills@example"} {
		if _, present := plugins[id]; present {
			t.Errorf("unrelated plugin %q should not be in enabledPlugins disable list, got %v", id, plugins)
		}
	}
	// Springfield should be disabled
	if got, present := plugins["springfield@brentguistwite"]; !present || got != false {
		t.Errorf("springfield@brentguistwite should be disabled, got present=%v val=%v", present, got)
	}
}

// TestResolvePluginDisablesUsesUserHomeDir verifies that the adapter resolves
// settings via os.UserHomeDir() + filepath.Join rather than a literal "~"
// expansion. Place a unique plugin ID in the temp HOME and assert it's picked
// up.
func TestResolvePluginDisablesUsesUserHomeDir(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	uniqueID := "springfield@unique-test-slug"
	writeSettingsJSON(t, homeDir, map[string]bool{
		uniqueID: true,
	})

	var warnBuf bytes.Buffer
	a := claude.NewWithOptions(nil, claude.Options{WarnWriter: &warnBuf})
	cmd := a.(agents.Commander).Command(agents.CommandInput{Prompt: "do work", WorkDir: "/tmp"})

	plugins := settingsEnabledPlugins(t, cmd.Args)
	if got := plugins[uniqueID]; got != false {
		t.Errorf("unique plugin ID %q not found disabled in emitted settings (HOME resolution may be broken)", uniqueID)
	}
	if warnBuf.Len() != 0 {
		t.Errorf("unexpected warning emitted (settings should be readable): %s", warnBuf.String())
	}
}

// containsStr is a test helper for substring checks.
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i+len(substr) <= len(s); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
