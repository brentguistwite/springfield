// release-sync propagates version.txt to every shipped manifest and
// regenerates hooks/checksums.txt so the plugin advertises checksums of
// binaries built with the deterministic release flags.
//
// Run: go run ./cmd/release-sync
//      go run ./cmd/release-sync -check   # fail if the working tree drifts after sync
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var manifestPaths = []string{
	".claude-plugin/plugin.json",
	".codex-plugin/plugin.json",
	".claude-plugin/marketplace.json",
	".agents/plugins/marketplace.json",
}

var releaseTargets = []struct {
	GOOS, GOARCH string
}{
	{"darwin", "amd64"},
	{"darwin", "arm64"},
	{"linux", "amd64"},
	{"linux", "arm64"},
}

var (
	versionFieldRe = regexp.MustCompile(`"version":\s*"[^"]*"`)
	semverRe       = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

func main() {
	check := flag.Bool("check", false, "fail with non-zero status if this run would write any file (idempotency guard)")
	skipBuild := flag.Bool("skip-build", false, "skip cross-compile + checksum regeneration; only sync manifests")
	flag.Parse()

	root, err := repoRoot()
	if err != nil {
		fail(err)
	}
	if err := os.Chdir(root); err != nil {
		fail(err)
	}

	version, err := readVersion()
	if err != nil {
		fail(err)
	}

	var changed []string
	for _, p := range manifestPaths {
		written, err := syncManifestVersion(p, version, *check)
		if err != nil {
			fail(fmt.Errorf("sync %s: %w", p, err))
		}
		if written {
			changed = append(changed, p)
		}
	}

	if !*skipBuild {
		written, err := regenerateChecksums(version, *check)
		if err != nil {
			fail(fmt.Errorf("regenerate checksums: %w", err))
		}
		if written {
			changed = append(changed, "hooks/checksums.txt")
		}
	}

	if *check && len(changed) > 0 {
		fmt.Fprintln(os.Stderr, "release-sync: -check failed; the following files would change:")
		for _, p := range changed {
			fmt.Fprintln(os.Stderr, "  "+p)
		}
		os.Exit(1)
	}

	fmt.Printf("release-sync: version=%s manifests=%d targets=%d changed=%d\n", version, len(manifestPaths), len(releaseTargets), len(changed))
}

func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not locate go.mod from %s", wd)
		}
		dir = parent
	}
}

func readVersion() (string, error) {
	data, err := os.ReadFile("version.txt")
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return "", fmt.Errorf("version.txt is empty")
	}
	if !semverRe.MatchString(v) {
		return "", fmt.Errorf("version.txt %q is not strict semver MAJOR.MINOR.PATCH", v)
	}
	return v, nil
}

func syncManifestVersion(path, version string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	matches := versionFieldRe.FindAll(data, -1)
	if len(matches) != 1 {
		return false, fmt.Errorf("expected exactly 1 \"version\" field in %s, found %d (refusing to silently rewrite ambiguous manifest)", path, len(matches))
	}
	target := fmt.Sprintf(`"version": %q`, version)
	updated := versionFieldRe.ReplaceAllString(string(data), target)
	if updated == string(data) {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	return true, os.WriteFile(path, []byte(updated), 0o644)
}

func regenerateChecksums(version string, dryRun bool) (bool, error) {
	dist, err := os.MkdirTemp("", "springfield-release-sync-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(dist)

	var lines []string
	for _, t := range releaseTargets {
		stage, err := os.MkdirTemp(dist, "stage-")
		if err != nil {
			return false, err
		}
		bin := filepath.Join(stage, "springfield")
		cmd := exec.Command(
			"go", "build",
			"-trimpath",
			"-buildvcs=false",
			"-ldflags", fmt.Sprintf("-s -w -X springfield/cmd.Version=v%s", version),
			"-o", bin,
			".",
		)
		cmd.Env = append(os.Environ(),
			"CGO_ENABLED=0",
			"GOOS="+t.GOOS,
			"GOARCH="+t.GOARCH,
		)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return false, fmt.Errorf("go build %s/%s: %w", t.GOOS, t.GOARCH, err)
		}
		sum, err := sha256File(bin)
		if err != nil {
			return false, err
		}
		archive := fmt.Sprintf("springfield_%s_%s_%s.tar.gz", version, t.GOOS, t.GOARCH)
		lines = append(lines, fmt.Sprintf("%s  ./%s", sum, archive))
	}

	out := strings.Join(lines, "\n") + "\n"
	existing, err := os.ReadFile("hooks/checksums.txt")
	if err == nil && string(existing) == out {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	return true, os.WriteFile("hooks/checksums.txt", []byte(out), 0o644)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "release-sync: %v\n", err)
	os.Exit(1)
}
