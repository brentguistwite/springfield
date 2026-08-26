package docs_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
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
// outside internal/core/agents may only construct an agents.Registry as
// exactly `agents.NewRegistry(catalog.DefaultAdapters(<lookPath>)...)`.
// Matching happens on the AST, so trailing comments and wrapper expressions
// like append(catalog.DefaultAdapters(lp), custom.New(lp)...) don't sneak past
// a substring check. Direct imports of per-agent subpackages remain allowed
// for narrow exported helpers (e.g. claude.ExtractCost) and tests.
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

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", relToRoot, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			// Matches the conventional (unaliased) agents import; an aliased
			// import would be its own review-worthy oddity.
			if !ok || pkgIdent.Name != "agents" || sel.Sel.Name != "NewRegistry" {
				return true
			}
			if isDefaultAdaptersSpread(call) {
				return true
			}
			t.Errorf("%s:%d assembles agents.NewRegistry outside catalog.DefaultAdapters; assembly must go through core/agents/catalog",
				filepath.ToSlash(relToRoot), fset.Position(call.Pos()).Line)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
}

// isDefaultAdaptersSpread reports whether call has the exact sanctioned shape
// agents.NewRegistry(catalog.DefaultAdapters(...)...) — one argument, spread,
// sourced from catalog.DefaultAdapters.
func isDefaultAdaptersSpread(call *ast.CallExpr) bool {
	if len(call.Args) != 1 || !call.Ellipsis.IsValid() {
		return false
	}
	inner, ok := call.Args[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := inner.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	return ok && pkgIdent.Name == "catalog" && sel.Sel.Name == "DefaultAdapters"
}

// TestDefaultAdaptersSpreadMatcher exercises the assembly-shape decision on
// synthetic snippets so a future loosening of the matcher fails this test
// instead of failing open against the real tree.
func TestDefaultAdaptersSpreadMatcher(t *testing.T) {
	cases := []struct {
		name       string
		expr       string
		sanctioned bool
	}{
		{"sanctioned spread", `agents.NewRegistry(catalog.DefaultAdapters(lp)...)`, true},
		{"bare registry, no adapters", `agents.NewRegistry()`, false},
		{"hand-assembled list", `agents.NewRegistry(claude.New(lp), codex.New(lp), gemini.New(lp))`, false},
		{"default set without spread", `agents.NewRegistry(catalog.DefaultAdapters(lp))`, false},
		{"extended via append", `agents.NewRegistry(append(catalog.DefaultAdapters(lp), custom.New(lp))...)`, false},
		{"wrapped then spread", `agents.NewRegistry(wrap(catalog.DefaultAdapters(lp))...)`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := parser.ParseExpr(tc.expr)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.expr, err)
			}
			call, ok := expr.(*ast.CallExpr)
			if !ok {
				t.Fatalf("%q is not a call expression", tc.expr)
			}
			if got := isDefaultAdaptersSpread(call); got != tc.sanctioned {
				t.Errorf("isDefaultAdaptersSpread(%q) = %t, want %t", tc.expr, got, tc.sanctioned)
			}
		})
	}
}
