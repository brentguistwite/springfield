package execution_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/execution"
)

func TestSetupWritesSpringfieldOwnedConfig(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)

	input := execution.Defaults()
	input.PlansDir = execution.TrackedPlansDir

	result, err := execution.Setup(root, []string{"claude", "codex"}, input)
	if err != nil {
		t.Fatalf("execution.Setup() error: %v", err)
	}
	if !result.Created {
		t.Fatal("expected Created=true")
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `"plans_dir": "springfield/plans"`) {
		t.Fatalf("expected Springfield tracked plans path, got:\n%s", content)
	}
	if !strings.Contains(content, `"single_workstream_iterations": 50`) {
		t.Fatalf("expected Springfield single_workstream_iterations key, got:\n%s", content)
	}
	if strings.Contains(content, "ralph_iterations") {
		t.Fatalf("did not expect legacy ralph_iterations key, got:\n%s", content)
	}
}

func TestIsTrackedPlansDirRejectsLegacyPath(t *testing.T) {
	if execution.IsTrackedPlansDir(".conductor/plans") {
		t.Fatal("expected legacy tracked path to be rejected")
	}
	if !execution.IsTrackedPlansDir(execution.TrackedPlansDir) {
		t.Fatal("expected canonical tracked path to be accepted")
	}
}

func writeProjectConfig(t *testing.T, root string) {
	t.Helper()

	body := `[project]
default_agent = "claude"
`
	if err := os.WriteFile(filepath.Join(root, "springfield.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write springfield.toml: %v", err)
	}
}
