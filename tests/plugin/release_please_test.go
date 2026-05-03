package plugin_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

type releasePleaseConfig struct {
	Packages map[string]struct {
		ExtraFiles []json.RawMessage `json:"extra-files"`
	} `json:"packages"`
}

type releasePleaseExtraFile struct {
	Type     string `json:"type"`
	Path     string `json:"path"`
	JSONPath string `json:"jsonpath"`
}

func releasePleaseExtraFiles(t *testing.T, root string) map[string]releasePleaseExtraFile {
	t.Helper()

	var cfg releasePleaseConfig
	if err := json.Unmarshal(readFile(t, root, "release-please-config.json"), &cfg); err != nil {
		t.Fatalf("parse release-please-config.json: %v", err)
	}

	pkg, ok := cfg.Packages["."]
	if !ok {
		t.Fatal("release-please-config.json missing root package")
	}

	files := make(map[string]releasePleaseExtraFile, len(pkg.ExtraFiles))
	for _, raw := range pkg.ExtraFiles {
		var path string
		if err := json.Unmarshal(raw, &path); err == nil {
			files[path] = releasePleaseExtraFile{Path: path}
			continue
		}

		var file releasePleaseExtraFile
		if err := json.Unmarshal(raw, &file); err != nil {
			t.Fatalf("parse extra-files entry %s: %v", raw, err)
		}
		if file.Path == "" {
			t.Fatalf("extra-files entry missing path: %s", raw)
		}
		files[file.Path] = file
	}
	return files
}

func TestReleasePleaseUpdatesVersionBearingReleaseArtifacts(t *testing.T) {
	root := repoRoot(t)
	files := releasePleaseExtraFiles(t, root)

	for _, rel := range []string{
		".claude-plugin/plugin.json",
		".codex-plugin/plugin.json",
	} {
		file, ok := files[rel]
		if !ok {
			t.Fatalf("release-please extra-files missing %s", rel)
		}
		if file.Type != "json" || file.JSONPath != "$.version" {
			t.Fatalf("%s extra-file = %+v, want json $.version updater", rel, file)
		}
	}

	for _, rel := range []string{
		".claude-plugin/marketplace.json",
		".agents/plugins/marketplace.json",
	} {
		file, ok := files[rel]
		if !ok {
			t.Fatalf("release-please extra-files missing %s", rel)
		}
		if file.Type != "json" || file.JSONPath != "$.plugins[0].version" {
			t.Fatalf("%s extra-file = %+v, want json $.plugins[0].version updater", rel, file)
		}
	}

	file, ok := files["hooks/checksums.txt"]
	if !ok {
		t.Fatal("release-please extra-files missing hooks/checksums.txt")
	}
	if file.Type != "generic" {
		t.Fatalf("hooks/checksums.txt extra-file = %+v, want generic updater", file)
	}
}

func TestChecksumsManifestCarriesReleasePleaseVersionAnnotations(t *testing.T) {
	root := repoRoot(t)
	text := string(readFile(t, root, "hooks/checksums.txt"))

	for _, marker := range []string{
		"x-release-please-start-version",
		"x-release-please-end",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("hooks/checksums.txt missing %q annotation", marker)
		}
	}

	start := strings.Index(text, "x-release-please-start-version")
	end := strings.Index(text, "x-release-please-end")
	if start == -1 || end == -1 || start > end {
		t.Fatal("hooks/checksums.txt release-please version annotation block is malformed")
	}
}

func TestReleaseWorkflowSkipsChecksumsAnnotationLines(t *testing.T) {
	root := repoRoot(t)
	workflow := string(readFile(t, root, ".github/workflows/release.yml"))

	if got := strings.Count(workflow, `"$expected" == \#*`); got < 3 {
		t.Fatalf("release.yml should skip checksum comment lines in each checksum loop, got %d guards", got)
	}
}

func TestChecksumsReaderIgnoresReleasePleaseAnnotations(t *testing.T) {
	root := repoRoot(t)
	entries := readChecksumsManifest(t, root)
	version := strings.TrimSpace(string(readFile(t, root, "version.txt")))

	want := []string{
		"./springfield_" + version + "_darwin_amd64.tar.gz",
		"./springfield_" + version + "_darwin_arm64.tar.gz",
		"./springfield_" + version + "_linux_amd64.tar.gz",
		"./springfield_" + version + "_linux_arm64.tar.gz",
	}
	for _, key := range want {
		if _, ok := entries[key]; !ok {
			t.Fatalf("checksums manifest missing %s", key)
		}
	}
	if len(entries) != len(want) {
		keys := make([]string, 0, len(entries))
		for key := range entries {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		t.Fatalf("checksums entries = %v, want only %v", keys, want)
	}
}
