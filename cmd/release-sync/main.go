// release-sync propagates version.txt to every shipped plugin/marketplace
// manifest so they stay in lock-step with the release tag.
//
// Run: go run ./cmd/release-sync
//
//	go run ./cmd/release-sync -check   # fail if the working tree drifts after sync
package main

import (
	"flag"
	"fmt"
	"os"
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

var (
	versionFieldRe = regexp.MustCompile(`"version":\s*"[^"]*"`)
	semverRe       = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

func main() {
	check := flag.Bool("check", false, "fail with non-zero status if this run would write any file (idempotency guard)")
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

	if *check && len(changed) > 0 {
		fmt.Fprintln(os.Stderr, "release-sync: -check failed; the following files would change:")
		for _, p := range changed {
			fmt.Fprintln(os.Stderr, "  "+p)
		}
		os.Exit(1)
	}

	fmt.Printf("release-sync: version=%s manifests=%d changed=%d\n", version, len(manifestPaths), len(changed))
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

func fail(err error) {
	fmt.Fprintf(os.Stderr, "release-sync: %v\n", err)
	os.Exit(1)
}
