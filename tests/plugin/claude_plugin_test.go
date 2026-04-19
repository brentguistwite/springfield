package plugin_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type pluginAuthor struct {
	Name string `json:"name"`
}

type pluginManifest struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Version     string       `json:"version"`
	Author      pluginAuthor `json:"author"`
	Homepage    string       `json:"homepage"`
	Repository  string       `json:"repository"`
	License     string       `json:"license"`
	Keywords    []string     `json:"keywords"`
}

type marketplaceManifest struct {
	Name  string `json:"name"`
	Owner struct {
		Name string `json:"name"`
	} `json:"owner"`
	Plugins []pluginManifest `json:"plugins"`
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve caller for repo root")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func readFile(t *testing.T, root, rel string) []byte {
	t.Helper()

	path := filepath.Join(root, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}

	return data
}

func readJSON[T any](t *testing.T, root, rel string) T {
	t.Helper()

	var value T
	if err := json.Unmarshal(readFile(t, root, rel), &value); err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	return value
}

func assertMentionsSpringfieldOnly(t *testing.T, rel, text string) {
	t.Helper()

	lower := strings.ToLower(text)
	if !strings.Contains(lower, "springfield") {
		t.Fatalf("%s should mention Springfield, got %q", rel, text)
	}

	for _, legacy := range []string{"ralph", "conductor", "tui"} {
		if strings.Contains(lower, legacy) {
			t.Fatalf("%s should not mention legacy term %q, got %q", rel, legacy, text)
		}
	}
}

func assertRequiredSkillsExist(t *testing.T, root string) {
	t.Helper()

	for _, rel := range []string{
		"skills/plan/SKILL.md",
		"skills/status/SKILL.md",
		"skills/recover/SKILL.md",
	} {
		path := filepath.Join(root, rel)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		if info.IsDir() {
			t.Fatalf("%s should be a file", rel)
		}

		text := string(readFile(t, root, rel))
		name := strings.TrimSuffix(filepath.Base(filepath.Dir(path)), filepath.Ext(filepath.Base(path)))
		if filepath.Base(filepath.Dir(path)) != name {
			name = filepath.Base(filepath.Dir(path))
		}
		for _, marker := range []string{
			"---\n",
			"name: " + filepath.Base(filepath.Dir(path)),
			"description:",
		} {
			if !strings.Contains(text, marker) {
				t.Fatalf("%s should contain %q, got:\n%s", rel, marker, text)
			}
		}
	}
}

func assertRequiredCommandsExist(t *testing.T, root string) {
	t.Helper()

	for _, rel := range []string{
		"commands/plan.md",
		"commands/status.md",
		"commands/recover.md",
	} {
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		if info.IsDir() {
			t.Fatalf("%s should be a file", rel)
		}

		text := string(readFile(t, root, rel))
		for _, marker := range []string{
			"---\n",
			"description:",
			"$ARGUMENTS",
		} {
			if !strings.Contains(text, marker) {
				t.Fatalf("%s should contain %q, got:\n%s", rel, marker, text)
			}
		}
	}
}

func TestRegenLoopOmitsStart(t *testing.T) {
	root := repoRoot(t)

	src, err := os.ReadFile(filepath.Join(root, "cmd", "regen", "main.go"))
	if err != nil {
		t.Fatalf("read cmd/regen/main.go: %v", err)
	}
	body := string(src)

	for _, want := range []string{`"plan"`, `"status"`, `"recover"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("cmd/regen/main.go must include %s in loop slice", want)
		}
	}
	if strings.Contains(body, `"start"`) {
		t.Fatal("cmd/regen/main.go must not include \"start\" in loop slice")
	}
}

func TestClaudePluginStructure(t *testing.T) {
	root := repoRoot(t)

	manifest := readJSON[pluginManifest](t, root, ".claude-plugin/plugin.json")
	if manifest.Name != "springfield" {
		t.Fatalf("plugin name = %q, want springfield", manifest.Name)
	}
	assertMentionsSpringfieldOnly(t, ".claude-plugin/plugin.json name", manifest.Name)
	assertMentionsSpringfieldOnly(t, ".claude-plugin/plugin.json description", manifest.Description)
	if manifest.Version == "" {
		t.Fatal("plugin version should not be empty")
	}
	if !slices.Contains(manifest.Keywords, "springfield") {
		t.Fatalf("plugin keywords = %v, want springfield", manifest.Keywords)
	}

	marketplace := readJSON[marketplaceManifest](t, root, ".claude-plugin/marketplace.json")
	if marketplace.Name != "brentguistwite" {
		t.Fatalf("marketplace name = %q, want brentguistwite", marketplace.Name)
	}
	if len(marketplace.Plugins) != 1 {
		t.Fatalf("marketplace plugin count = %d, want 1", len(marketplace.Plugins))
	}
	rawMarketplace := string(readFile(t, root, ".claude-plugin/marketplace.json"))
	if strings.Contains(rawMarketplace, `"description": "Marketplace metadata for the Springfield plugin"`) {
		t.Fatal("marketplace manifest should omit the root description key so Claude validation passes")
	}

	entry := marketplace.Plugins[0]
	if entry.Name != manifest.Name {
		t.Fatalf("marketplace plugin name = %q, want %q", entry.Name, manifest.Name)
	}
	if entry.Version != manifest.Version {
		t.Fatalf("marketplace plugin version = %q, want %q", entry.Version, manifest.Version)
	}
	assertMentionsSpringfieldOnly(t, ".claude-plugin/marketplace.json plugin description", entry.Description)

	assertRequiredSkillsExist(t, root)
	assertRequiredCommandsExist(t, root)

	// start skill and command must be absent (removed to prevent subagent recursion)
	if _, err := os.Stat(filepath.Join(root, "skills", "start")); !os.IsNotExist(err) {
		t.Fatalf("skills/start/ must be absent after removal, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "commands", "start.md")); !os.IsNotExist(err) {
		t.Fatalf("commands/start.md must be absent after removal, stat err=%v", err)
	}
}
