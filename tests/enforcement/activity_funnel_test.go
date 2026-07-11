package enforcement_test

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

// activityWriterFunc is the ONE function permitted to mutate a plan's in-flight
// PlanActivity signal. Every phase-execution site (the story loop, the review
// gate, the verify gate, merge) must route its stamp through it: enterPhase is
// where the SaveState + single-writer discipline lives. A direct write anywhere
// else skips the persist AND can strand a stale phase after the site returns —
// exactly the lie the Activity contract forbids ("a stale value is worse than
// none"). This gate makes that funnel structural, not prose: it is built BEFORE
// the review/verify counter stories so the moment one of those gates tries to
// stamp a round directly, the build goes red and forces it back through here.
const activityWriterFunc = "enterPhase"

// activityWritePkgs are the source dirs where a running plan executes phases and
// could therefore stamp PlanActivity: the runner + its gates (planrun) and the
// type's home (conductor). The status projection (statusview) only READS the
// signal and is intentionally out of scope — it never mutates PlanState.
var activityWritePkgs = []string{
	"../../internal/features/conductor/planrun",
	"../../internal/features/conductor",
}

// activityWriteViolations returns one formatted finding per direct PlanActivity
// write in f that is NOT lexically inside activityWriterFunc. Two structural
// shapes count as a write, matching the two ways the in-flight signal is set:
//
//   - a field assignment whose LHS selects .Activity  (e.g. ps.Activity = …)
//   - a composite literal of type PlanActivity         (PlanActivity{…} / &PlanActivity{…})
//
// Keying construction on the *type name* (not the field key) is what keeps the
// scan from false-positiving on the read side's StatusView{Activity: …} — that
// is a KeyValueExpr key on a different type, not a selector assignment.
//
// label is used only for the human-facing finding location.
func activityWriteViolations(fset *token.FileSet, f *ast.File, label string) []string {
	var out []string
	report := func(fnName string, n ast.Node) {
		pos := fset.Position(n.Pos())
		file := pos.Filename
		if file == "" {
			file = label
		}
		out = append(out, fmt.Sprintf("%s:%d: PlanActivity written in %s()", file, pos.Line, fnName))
	}

	inspectBody := func(fnName string, body ast.Node) {
		ast.Inspect(body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range x.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel != nil && sel.Sel.Name == "Activity" {
						report(fnName, x)
					}
				}
			case *ast.CompositeLit:
				if isPlanActivityType(x.Type) {
					report(fnName, x)
				}
			}
			return true
		})
	}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.Name == activityWriterFunc && d.Recv == nil {
				// The sole sanctioned writer: the free FUNCTION enterPhase is exempt
				// by definition. A METHOD named enterPhase (d.Recv != nil) on some
				// other type is NOT the funnel and must still be scanned — otherwise
				// a same-named method could stamp Activity directly and bypass the
				// guard silently.
				continue
			}
			if d.Body != nil {
				inspectBody(d.Name.Name, d.Body)
			}
		default:
			// Package-level constructions (var x = PlanActivity{…}) also bypass
			// the funnel; catch them under a synthetic scope name.
			inspectBody("(package-level)", d)
		}
	}
	return out
}

// isPlanActivityType reports whether a composite-literal type expression names
// the PlanActivity struct, in either the bare (PlanActivity{}, from inside the
// conductor package) or qualified (conductor.PlanActivity{}) form. A leading &
// is a UnaryExpr wrapping the CompositeLit, so the literal's own Type node is
// still the Ident/SelectorExpr checked here.
func isPlanActivityType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == "PlanActivity"
	case *ast.SelectorExpr:
		return t.Sel != nil && t.Sel.Name == "PlanActivity"
	}
	return false
}

// TestEnterPhaseIsSoleActivityWriter is the guarantee: an AST scan of every
// phase-execution package fails the build if any site writes PlanActivity
// outside enterPhase. With the story loop wired through the funnel and the gates
// not yet stamping, this is clean today; it turns red the instant a later gate
// stamps a round directly instead of calling enterPhase.
func TestEnterPhaseIsSoleActivityWriter(t *testing.T) {
	fset := token.NewFileSet()
	var violations []string
	var enterPhaseDecls []string
	for _, pkg := range activityWritePkgs {
		entries, err := os.ReadDir(pkg)
		if err != nil {
			t.Fatalf("read pkg %s: %v", pkg, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(pkg, e.Name())
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			violations = append(violations, activityWriteViolations(fset, f, path)...)
			for _, decl := range f.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == activityWriterFunc && fn.Recv == nil {
					enterPhaseDecls = append(enterPhaseDecls, fmt.Sprintf("%s:%d", path, fset.Position(fn.Pos()).Line))
				}
			}
		}
	}
	if len(violations) > 0 {
		t.Errorf("PlanActivity written outside %s() — every phase-execution site must funnel through it (route the stamp via %s so it persists and cannot strand a stale phase):\n  %s",
			activityWriterFunc, activityWriterFunc, strings.Join(violations, "\n  "))
	}
	// The exemption above is keyed on the NAME enterPhase; if that free function
	// disappeared the whole guard would pass vacuously, and if a second one
	// appeared the funnel would no longer be single-writer. Pin it to exactly one.
	if len(enterPhaseDecls) != 1 {
		t.Errorf("expected exactly ONE free-function %s() declaration across %v, found %d: %v — the single-writer funnel and its exemption depend on there being exactly one",
			activityWriterFunc, activityWritePkgs, len(enterPhaseDecls), enterPhaseDecls)
	}
}

// TestActivityWriteScanCatchesBypass is the negative case that keeps the gate
// honest: it feeds the SAME detector a synthetic phase-execution site that
// stamps Activity directly instead of calling enterPhase, and asserts the scan
// flags it. If the detector ever went blind, TestEnterPhaseIsSoleActivityWriter
// would pass vacuously and a real bypass would ship — this proves it does not.
func TestActivityWriteScanCatchesBypass(t *testing.T) {
	// Both write shapes a real gate might use to skip the funnel.
	cases := map[string]string{
		"field-assignment": `package planrun

func runVerifyGate(ps *conductor.PlanState) {
	ps.Activity = &conductor.PlanActivity{Phase: "verifying", Round: 1}
}
`,
		"struct-construction": `package planrun

func stamp(ps *conductor.PlanState) {
	*ps = conductor.PlanState{Activity: nil}
	_ = conductor.PlanActivity{Phase: "reviewing"}
}
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "bypass.go", src, 0)
			if err != nil {
				t.Fatalf("parse synthetic bypass: %v", err)
			}
			if got := activityWriteViolations(fset, f, "bypass.go"); len(got) == 0 {
				t.Fatalf("detector failed to flag a direct Activity write outside %s() — the coverage gate is blind", activityWriterFunc)
			}
		})
	}
}

// TestActivityWriteScanAllowsEnterPhase is the positive control: the detector
// must NOT flag a write that lives inside enterPhase, or the real scan above
// would be a false alarm the moment it parsed activity.go.
func TestActivityWriteScanAllowsEnterPhase(t *testing.T) {
	const src = `package planrun

func enterPhase(ps *conductor.PlanState) {
	ps.Activity = &conductor.PlanActivity{Phase: "implementing", Round: 1}
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "activity.go", src, 0)
	if err != nil {
		t.Fatalf("parse synthetic enterPhase: %v", err)
	}
	if got := activityWriteViolations(fset, f, "activity.go"); len(got) != 0 {
		t.Fatalf("detector flagged a sanctioned write inside %s(): %v", activityWriterFunc, got)
	}
}
