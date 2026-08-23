package docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestArchitectureDocTracksWiring pins docs/architecture.md to the structural
// facts it documents. The architecture map is load-bearing for agents: it is
// the only page explaining how cmd/, internal/features/, and internal/core/
// wire together, so its anchors must track reality. A rename that breaks an
// anchor here means the doc needs updating in the same change.
func TestArchitectureDocTracksWiring(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "docs", "architecture.md")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read docs/architecture.md: %v (the architecture map is load-bearing documentation — restore or recreate it rather than deleting)", err)
	}
	content := string(data)

	required := []string{
		"cmd/root.go",          // single command-registration surface
		"internal/features",    // feature packages layer
		"internal/core/agents", // adapter boundary
		"internal/core/exec",   // subprocess engine
		"internal/storage",     // control-plane state layout
		"docs/prd-format.md",   // envelope contract pointer
	}
	for _, phrase := range required {
		if !strings.Contains(content, phrase) {
			t.Errorf("docs/architecture.md missing required anchor %q; the doc must keep tracking actual wiring", phrase)
		}
	}

	// The module's package entry-file convention is index.go / doc.go (see
	// AGENTS.md Principle 3); no package.go file exists anywhere. A doc
	// claiming the package.go idiom would misdirect every agent creating a
	// new package — the exact drift this suite exists to catch.
	if strings.Contains(content, "package.go") {
		t.Errorf(`docs/architecture.md references "package.go"; the module's entry-file convention is index.go / doc.go`)
	}
}

// TestAdapterAssemblyConfinedToCatalog pins the architecture map's claim that
// agent adapters are assembled only in core/agents/catalog: production code
// outside internal/core/agents may not construct an agents.Registry itself.
// A hand-assembled registry bypasses the canonical order and capability set
// catalog.DefaultAdapters guarantees while docs/architecture.md still promises
// single-point assembly. Direct imports of the per-agent subpackages remain
// allowed for narrow exported helpers (e.g. claude.ExtractCost) and tests.
func TestAdapterAssemblyConfinedToCatalog(t *testing.T) {
	root := repoRoot(t)

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".worktrees", ".claude", ".springfield", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		relToRoot, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.ToSlash(relToRoot), "internal/core/agents/") {
			return nil // adapters and their catalog are allowed to know each other
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "agents.NewRegistry(") && !strings.Contains(line, "catalog.DefaultAdapters") {
				t.Errorf("%s:%d constructs agents.NewRegistry directly outside internal/core/agents; assembly must go through core/agents/catalog.DefaultAdapters", relToRoot, i+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
}
