package plugin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexPluginMatchesSpringfieldMetadata(t *testing.T) {
	root := repoRoot(t)

	claude := readJSON[pluginManifest](t, root, ".claude-plugin/plugin.json")
	codex := readJSON[pluginManifest](t, root, ".codex-plugin/plugin.json")

	if codex.Name != claude.Name {
		t.Fatalf("codex plugin name = %q, want %q", codex.Name, claude.Name)
	}
	if codex.Version != claude.Version {
		t.Fatalf("codex plugin version = %q, want %q", codex.Version, claude.Version)
	}
	if codex.Description != claude.Description {
		t.Fatalf("codex plugin description = %q, want %q", codex.Description, claude.Description)
	}
	assertMentionsSpringfieldOnly(t, ".codex-plugin/plugin.json name", codex.Name)
	assertMentionsSpringfieldOnly(t, ".codex-plugin/plugin.json description", codex.Description)
}

func TestReleaseMetadataStaysPluginFirst(t *testing.T) {
	root := repoRoot(t)

	const wantFormulaDesc = `desc "Plugin-first local CLI for Springfield setup and workflow control"`

	workflow := string(readFile(t, root, ".github/workflows/release.yml"))
	if !strings.Contains(workflow, `go test ./tests/plugin/...`) {
		t.Fatal("release workflow should validate plugin metadata before packaging")
	}
	for _, want := range []string{
		"hooks/checksums.txt",
		"-xzf \"dist/$asset\" springfield",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("release workflow should enforce committed checksum manifest: want %q", want)
		}
	}
	for _, stale := range []string{
		"dist/checksums.txt",
		"sha256sum -c checksums.txt",
	} {
		if strings.Contains(workflow, stale) {
			t.Fatalf("release workflow should not use stale release checksums asset flow: found %q", stale)
		}
	}
	if !strings.Contains(workflow, wantFormulaDesc) {
		t.Fatalf("release workflow should render plugin-first formula description: want %s", wantFormulaDesc)
	}
	if strings.Contains(workflow, "Local-first CLI and TUI") {
		t.Fatal("release workflow still contains stale TUI-era formula wording")
	}

	formula := string(readFile(t, root, "Formula/springfield.rb"))
	if !strings.Contains(formula, wantFormulaDesc) {
		t.Fatalf("formula template should stay aligned with release wording: want %s", wantFormulaDesc)
	}

	releaseDoc := string(readFile(t, root, "docs/release.md"))
	for _, rel := range []string{
		".claude-plugin/plugin.json",
		".claude-plugin/marketplace.json",
		".codex-plugin/plugin.json",
		"hooks/checksums.txt",
	} {
		if !strings.Contains(releaseDoc, rel) {
			t.Fatalf("release doc should mark %s as release-critical", rel)
		}
	}
	if !strings.Contains(strings.ToLower(releaseDoc), "release-critical") {
		t.Fatal("release doc should explicitly call plugin metadata release-critical")
	}
	if !strings.Contains(strings.ToLower(releaseDoc), "plugin-shipped") {
		t.Fatal("release doc should explain that the hook trusts plugin-shipped checksums")
	}
	if strings.Contains(releaseDoc, "- `checksums.txt`") {
		t.Fatal("release doc should not list checksums.txt as a published release asset")
	}

	readme := string(readFile(t, root, "README.md"))
	if !strings.Contains(readme, "plugin-shipped") || !strings.Contains(readme, "hooks/checksums.txt") {
		t.Fatal("README should explain that SessionStart trusts plugin-shipped hooks/checksums.txt")
	}
	if strings.Contains(readme, "- `checksums.txt`") {
		t.Fatal("README should not list checksums.txt as a published release asset")
	}

	for _, rel := range []string{
		".claude-plugin/plugin.json",
		".claude-plugin/marketplace.json",
		".codex-plugin/plugin.json",
	} {
		path := filepath.Join(root, rel)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		if info.IsDir() {
			t.Fatalf("%s should be a file", rel)
		}
	}
}
