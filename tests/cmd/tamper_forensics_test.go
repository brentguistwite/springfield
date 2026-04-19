package cmd_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTamperWritesForensicsSidecar verifies that a tamper event lands a
// timestamped forensics JSON file alongside the state-tampered archive.
func TestTamperWritesForensicsSidecar(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}

	plansRoot := filepath.Join(dir, ".springfield", "plans")
	fakeBinDir := filepath.Join(dir, "bin")
	installTamperingAgent(t, fakeBinDir, "claude",
		fmt.Sprintf("for f in %s/*/batch.json; do echo 'garbage' > \"$f\"; done", plansRoot))

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected tamper, got:\n%s", output)
	}

	archiveDir := filepath.Join(dir, ".springfield", "archive")
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("read archive dir: %v", err)
	}

	var sidecarPath string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tamper.json") {
			sidecarPath = filepath.Join(archiveDir, e.Name())
			break
		}
	}
	if sidecarPath == "" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected a *.tamper.json sidecar, entries=%v", names)
	}

	data, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}

	var sidecar map[string]any
	if err := json.Unmarshal(data, &sidecar); err != nil {
		t.Fatalf("decode sidecar %s: %v\nraw=%s", sidecarPath, err, data)
	}

	for _, key := range []string{"batch_id", "reason", "agent_id", "detected_at"} {
		if _, ok := sidecar[key]; !ok {
			t.Errorf("sidecar missing %q field; have keys: %v", key, mapKeys(sidecar))
		}
	}
	if reason, _ := sidecar["reason"].(string); !strings.Contains(reason, "batch.json") {
		t.Errorf("sidecar reason = %q, want to mention batch.json", reason)
	}
}

// TestTamperForensicsSidecarUniqueAcrossRuns verifies the unix-nano suffix
// keeps sidecars from colliding across two distinct tamper events.
func TestTamperForensicsSidecarUniqueAcrossRuns(t *testing.T) {
	bin := buildBinary(t)

	run := func() string {
		dir := t.TempDir()
		writeSpringfieldConfig(t, dir, "claude")
		if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Do the thing"); err != nil {
			t.Fatalf("plan: %v", err)
		}
		plansRoot := filepath.Join(dir, ".springfield", "plans")
		fakeBinDir := filepath.Join(dir, "bin")
		installTamperingAgent(t, fakeBinDir, "claude",
			fmt.Sprintf("for f in %s/*/batch.json; do echo 'bad' > \"$f\"; done", plansRoot))
		_, _ = runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")

		archiveDir := filepath.Join(dir, ".springfield", "archive")
		entries, _ := os.ReadDir(archiveDir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tamper.json") {
				return e.Name()
			}
		}
		t.Fatalf("no sidecar produced")
		return ""
	}

	a := run()
	b := run()
	if a == b {
		t.Errorf("expected unique sidecar names, both = %q", a)
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
