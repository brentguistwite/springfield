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
