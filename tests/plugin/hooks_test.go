package plugin_test

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHooksJSONShape(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var doc struct {
		Description string `json:"description"`
		Hooks       struct {
			SessionStart []struct {
				Hooks []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"SessionStart"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Hooks.SessionStart) == 0 {
		t.Fatal("SessionStart hook missing")
	}
	if len(doc.Hooks.SessionStart[0].Hooks) == 0 {
		t.Fatal("SessionStart[0].hooks empty")
	}
	if doc.Hooks.SessionStart[0].Hooks[0].Type != "command" {
		t.Fatalf("type = %q, want command", doc.Hooks.SessionStart[0].Hooks[0].Type)
	}
	cmd := doc.Hooks.SessionStart[0].Hooks[0].Command
	want := "${CLAUDE_PLUGIN_ROOT}/hooks/session-start.sh"
	if cmd != want {
		t.Fatalf("command = %q, want %q", cmd, want)
	}
}

func TestSessionStartScriptExecutable(t *testing.T) {
	root := repoRoot(t)
	info, err := os.Stat(filepath.Join(root, "hooks", "session-start.sh"))
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("script not executable: mode=%v", info.Mode())
	}
}

func readChecksumsManifest(t *testing.T, root string) map[string]string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, "hooks", "checksums.txt"))
	if err != nil {
		t.Fatalf("read checksums.txt: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	entries := make(map[string]string, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("invalid checksums.txt line %q", line)
		}
		entries[fields[1]] = fields[0]
	}
	return entries
}

func buildReleaseArchive(t *testing.T, root, version, goos, goarch, distDir string) string {
	t.Helper()

	stage := t.TempDir()
	bin := filepath.Join(stage, "springfield")
	build := exec.Command(
		"go",
		"build",
		"-trimpath",
		"-ldflags=-s -w -X springfield/cmd.Version=v"+version,
		"-o",
		bin,
		".",
	)
	build.Dir = root
	build.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS="+goos,
		"GOARCH="+goarch,
	)
	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("go build %s/%s: %v\n%s", goos, goarch, err, out)
	}

	archive := filepath.Join(distDir, fmt.Sprintf("springfield_%s_%s_%s.tar.gz", version, goos, goarch))
	tarCmd := exec.Command("tar", "-C", stage, "-czf", archive, "springfield")
	out, err = tarCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tar %s/%s: %v\n%s", goos, goarch, err, out)
	}
	return archive
}

func sha256TarMember(t *testing.T, archivePath, member string) string {
	t.Helper()

	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open archive %s: %v", archivePath, err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader %s: %v", archivePath, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read tar %s: %v", archivePath, err)
		}
		if hdr.Name != member {
			continue
		}
		h := sha256.New()
		if _, err := io.Copy(h, tr); err != nil {
			t.Fatalf("hash tar member %s in %s: %v", member, archivePath, err)
		}
		return fmt.Sprintf("%x", h.Sum(nil))
	}

	t.Fatalf("archive %s missing member %s", archivePath, member)
	return ""
}

func TestSessionStartChecksumsManifestMatchesReleaseBinaries(t *testing.T) {
	root := repoRoot(t)
	manifest := readJSON[pluginManifest](t, root, ".claude-plugin/plugin.json")
	entries := readChecksumsManifest(t, root)
	distDir := t.TempDir()

	targets := []struct {
		goos   string
		goarch string
	}{
		{goos: "darwin", goarch: "amd64"},
		{goos: "darwin", goarch: "arm64"},
		{goos: "linux", goarch: "amd64"},
		{goos: "linux", goarch: "arm64"},
	}
	for _, target := range targets {
		archive := buildReleaseArchive(t, root, manifest.Version, target.goos, target.goarch, distDir)
		key := "./" + filepath.Base(archive)
		want, ok := entries[key]
		if !ok {
			t.Fatalf("checksums.txt missing %q", key)
		}
		got := sha256TarMember(t, archive, "springfield")
		if got != want {
			t.Fatalf("checksums.txt[%q] = %s, want extracted-binary sha %s", key, want, got)
		}
	}

	if len(entries) != len(targets) {
		t.Fatalf("checksums.txt entry count = %d, want %d", len(entries), len(targets))
	}
}

func TestSessionStartShellBehavior(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	root := repoRoot(t)
	cmd := exec.Command("bash", filepath.Join(root, "hooks", "tests", "run.sh"))
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hooks/tests/run.sh failed: %v\n%s", err, out)
	}
}

func TestSessionStartShellcheck(t *testing.T) {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("shellcheck not installed")
	}
	root := repoRoot(t)
	cmd := exec.Command(
		"shellcheck",
		"--severity=warning",
		filepath.Join(root, "hooks", "session-start.sh"),
		filepath.Join(root, "hooks", "tests", "run.sh"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shellcheck failed: %s", out)
	}
}
