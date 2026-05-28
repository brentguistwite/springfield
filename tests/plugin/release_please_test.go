package plugin_test

import (
	"encoding/json"
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

	// The plugin no longer ships hooks/checksums.txt, so release-please must
	// not carry a generic updater for it.
	if _, ok := files["hooks/checksums.txt"]; ok {
		t.Fatal("release-please extra-files should no longer reference hooks/checksums.txt")
	}
}
