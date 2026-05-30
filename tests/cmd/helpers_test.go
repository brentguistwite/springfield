package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/features/conductor"
)

func writeSpringfieldConfig(t *testing.T, dir string, agent string) {
	t.Helper()

	// allow_protected_base = true so the protected-base guard does not block
	// integration tests that init their tempdir repos on the default "main"
	// branch. auto_branch = false so the auto-branch flow does not insert a
	// surprise feature-branch cut into tests that pre-date that feature.
	// Both behaviors are exercised in dedicated end-to-end tests via the
	// strict and refuse-protected helpers below.
	content := "[project]\nagent_priority = [\"" + agent + "\"]\nallow_protected_base = true\nauto_branch = false\n"
	path := filepath.Join(dir, "springfield.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write springfield.toml: %v", err)
	}
}

// writeSpringfieldConfigRefuseProtected writes a project config that opts
// out of auto-branching but does NOT opt out of the protected-base guard.
// Used by end-to-end tests that exercise the legacy refusal behavior on a
// tempdir repo initialized on the "main" branch.
func writeSpringfieldConfigRefuseProtected(t *testing.T, dir string, agent string) {
	t.Helper()

	content := "[project]\nagent_priority = [\"" + agent + "\"]\nauto_branch = false\n"
	path := filepath.Join(dir, "springfield.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write springfield.toml: %v", err)
	}
}

// writeSpringfieldConfigStrict writes a project config that uses every
// default — auto-branching enabled, protected-base guard enabled. Used by
// end-to-end tests that exercise the canonical first-run experience on a
// tempdir repo initialized on the "main" branch.
func writeSpringfieldConfigStrict(t *testing.T, dir string, agent string) {
	t.Helper()

	content := "[project]\nagent_priority = [\"" + agent + "\"]\n"
	path := filepath.Join(dir, "springfield.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write springfield.toml: %v", err)
	}
}

func writeConductorConfigBinary(t *testing.T, root string, cfg *conductor.Config) {
	t.Helper()

	dir := filepath.Join(root, ".springfield", "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir execution: %v", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal conductor config: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644); err != nil {
		t.Fatalf("write execution config: %v", err)
	}
}

func writePlanFileBinary(t *testing.T, root, plansDir, name, content string) {
	t.Helper()

	dir := filepath.Join(root, plansDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write plan file: %v", err)
	}
}

// writePRDJSON writes a minimal prd.json under .springfield/plans/<planID>/prd.json.
// Use this when a conductor config registers a PRD-style path (.../<id>/prd.json);
// writePlanFileBinary unconditionally appends ".md" and is the wrong helper for
// that shape.
func writePRDJSON(t *testing.T, root, planID string) {
	t.Helper()
	dir := filepath.Join(root, ".springfield", "plans", planID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := `{"id":"` + planID + `","user_stories":[]}`
	if err := os.WriteFile(filepath.Join(dir, "prd.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write prd.json: %v", err)
	}
}

// writePRDJSONAllPassed writes a prd.json with one already-passing story.
// Used by tests that exercise recover --mark-completed, which validates that
// every story in prd.json has passes=true before flipping plan state.
func writePRDJSONAllPassed(t *testing.T, root, planID string) {
	t.Helper()
	dir := filepath.Join(root, ".springfield", "plans", planID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := `{"id":"` + planID + `","title":"` + planID + `","user_stories":[{"id":"US-001","title":"Story 1","description":"d","acceptance_criteria":["a"],"priority":1,"passes":true}]}`
	if err := os.WriteFile(filepath.Join(dir, "prd.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write prd.json: %v", err)
	}
}
