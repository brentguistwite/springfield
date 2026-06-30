package enforcement_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	coreexec "springfield/internal/core/exec"
	"springfield/internal/features/conductor/planreview"
	"springfield/internal/features/conductor/planrun"
	"springfield/internal/testsupport/fixtures"
)

func loadCap(t *testing.T, tool, scenario string) []coreexec.Event {
	t.Helper()
	return fixtures.LoadEvents(t, filepath.Join(realcapturesDir, tool, scenario+".jsonl"))
}

// TestTransportParsersExercisedAgainstRealCaptures is the coverage half of the
// gate: each transport parser that interprets an agent's stream-json output is
// run against REAL captured bytes, not a hand-authored struct. This is the
// behavior the 2026-06-03 gate bugs lacked.
func TestTransportParsersExercisedAgainstRealCaptures(t *testing.T) {
	claudeRev := loadCap(t, "claude", "reviewer-verdict-pass-no-tools")
	codexRev := loadCap(t, "codex", "reviewer-verdict-pass-no-tools")
	claudeImpl := loadCap(t, "claude", "implementer-story-pass")

	claudeDec := claude.New(osexec.LookPath).(agents.TranscriptDecoder)
	codexDec := codex.New(osexec.LookPath).(agents.TranscriptDecoder)

	// AssistantText: decodes the verdict out of each tool's transport shape.
	for _, tc := range []struct {
		name string
		text string
	}{
		{"claude", claudeDec.AssistantText(claudeRev)},
		{"codex", codexDec.AssistantText(codexRev)},
	} {
		if !strings.Contains(tc.text, "<review-verdict>pass</review-verdict>") {
			t.Fatalf("%s AssistantText missing verdict marker: %q", tc.name, tc.text)
		}
	}

	// ScanReviewVerdict: over decoded text, finds the pass.
	if v, found := planreview.ScanReviewVerdict([]coreexec.Event{{Type: coreexec.EventStdout, Data: claudeDec.AssistantText(claudeRev)}}); !found || v.Class != planreview.VerdictPass {
		t.Fatalf("ScanReviewVerdict on real decoded text: found=%v class=%q", found, v.Class)
	}

	// ValidateResult: a tool-free reviewer transcript passes in reviewer mode.
	if err := claude.New(osexec.LookPath).(agents.ResultValidator).ValidateResult(coreexec.Result{ExitCode: 0, Events: claudeRev}, false); err != nil {
		t.Fatalf("ValidateResult reviewer-mode on real capture: %v", err)
	}

	// ScanMarkers: over a real transcript carrying story-pass + COMPLETE.
	passed, complete := planrun.ScanMarkers(claudeImpl)
	if !complete || !slices.Contains(passed, "US-001") {
		t.Fatalf("ScanMarkers on real transcript: passed=%v complete=%v", passed, complete)
	}
}

// parserPkgs are the packages whose functions interpret agent stream-json
// output. Any function here that takes a coreexec.Event slice or coreexec.Result
// is a transport parser and must be registered below (covered or exempt).
var parserPkgs = []string{
	"../../internal/core/agents/claude",
	"../../internal/core/agents/codex",
	"../../internal/core/agents/gemini",
	"../../internal/core/runtime",
	"../../internal/features/conductor/planreview",
	"../../internal/features/conductor/planrun",
}

// coveredParsers are exercised against a real capture (here or in a sibling
// real-capture test). Keyed by function/method name.
var coveredParsers = map[string]bool{
	"ValidateResult":    true, // reviewer_validation_test + above (real captures)
	"AssistantText":     true, // transcript_decoder_test + above (claude+codex captures)
	"ScanReviewVerdict": true, // transcript_decoder_test + above (decoded real text)
	"ScanMarkers":       true, // above (real implementer-story-pass transcript)
}

// exemptParsersByPkg is checked BEFORE the bare-name maps, keyed by
// "<pkgdir>.<func>". It exists because the bare-name maps mark a function
// covered across EVERY adapter that defines it — so a real claude+codex capture
// silently vouches for an unverified gemini/runner implementation of the same
// method. These entries pin those package-specific cases honestly.
var exemptParsersByPkg = map[string]string{
	"gemini.AssistantText":  "capture-pending: gemini CLI unavailable, no real transcript to capture; claude+codex AssistantText are real-capture covered",
	"gemini.ValidateResult": "capture-pending: gemini CLI unavailable; claude+codex ValidateResult are real-capture covered",
	"runtime.AssistantText": "delegates to the resolved adapter's TranscriptDecoder (claude/codex real-capture covered); no transport parsing of its own",
}

// exemptParsers consume events/results but do NOT decode raw transport text, so
// synthetic input is legitimate — OR a real capture is pending. The reason is
// the documentation; a bare entry is not allowed.
var exemptParsers = map[string]string{
	"ClassifyError":          "capture-pending: needs a real rate-limit/api-error transcript (cannot force on demand); covered by synthetic structured-needle tests for now",
	"EnforceTurnCap":         "operates on num_turns already extracted by scanNumTurns; turn-cap math is unit-tested",
	"scanNumTurns":           "capture-pending: corpus result events carry num_turns:1, too low to exercise a positive cap; needs a high-turn transcript",
	"iterationWorkComplete":  "delegates transport parsing to ScanMarkers (a covered parser); own logic operates only on the returned marker IDs",
	"iterationStoryComplete": "delegates transport parsing to ScanMarkers (a covered parser); own logic operates only on the returned marker IDs + a git HEAD compare",
	"verdictScanEvents":      "wiring helper: decode+wrap, exercised end-to-end by the Review real-Runner integration test",
	"Cooldown":               "capture-pending: parses the rate-limit reset timestamp from a real rate-limit transcript (cannot force on demand); structured needles unit-tested",
	"parseCooldown":          "internal helper of Cooldown — same capture-pending rate-limit transcript",
	"collectLines":           "stdout line-collection helper for cooldown parsing; no transport-semantic parsing of its own",
	"ExtractCost":            "capture-pending: parses usage/cost from result events; cost-capture coverage is out of scope for the gate-hardening effort",
	"extractCost":            "capture-pending: planrun cost rollup; same as ExtractCost",
	"writeReviewEvidence":    "persists reviewer events to the evidence dir — no semantic parsing of the transport",
}

// TestEveryTransportParserIsRegistered is the staleness half: a go/ast scan of
// the parser packages finds every function that takes a coreexec.Event slice or
// coreexec.Result, and fails if any is neither covered nor exempt. Adding a new
// transport parser without a real-capture test (or an explicit exemption with a
// reason) turns CI red — that is what makes "test parsers against real output"
// always-followed rather than prose.
func TestEveryTransportParserIsRegistered(t *testing.T) {
	fset := token.NewFileSet()
	for _, pkg := range parserPkgs {
		entries, err := os.ReadDir(pkg)
		if err != nil {
			t.Fatalf("read pkg %s: %v", pkg, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, filepath.Join(pkg, e.Name()), nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", e.Name(), err)
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Type.Params == nil {
					continue
				}
				if !takesAgentOutput(fn.Type.Params.List) {
					continue
				}
				name := fn.Name.Name
				qualified := filepath.Base(pkg) + "." + name
				if _, ok := exemptParsersByPkg[qualified]; ok {
					continue
				}
				if coveredParsers[name] {
					continue
				}
				if _, ok := exemptParsers[name]; ok {
					continue
				}
				t.Errorf("transport parser %q (%s) consumes agent output but is neither covered by a real-capture test nor exempt.\n"+
					"Add it to coveredParsers with a tests/realcaptures fixture, or to exemptParsers with a reason.",
					name, filepath.Join(pkg, e.Name()))
			}
		}
	}
}

// takesAgentOutput reports whether any param is a []coreexec.Event (or
// []exec.Event) or a coreexec.Result (exec.Result) — the two ways a function
// receives an agent's raw stream-json output. Qualifier is pinned to the exec
// package so unrelated Result types (e.g. coreruntime.Result) don't match.
func takesAgentOutput(params []*ast.Field) bool {
	isExecQual := func(x ast.Expr) bool {
		id, ok := x.(*ast.Ident)
		return ok && (id.Name == "exec" || id.Name == "coreexec")
	}
	for _, p := range params {
		switch typ := p.Type.(type) {
		case *ast.ArrayType:
			if sel, ok := typ.Elt.(*ast.SelectorExpr); ok && sel.Sel.Name == "Event" && isExecQual(sel.X) {
				return true
			}
		case *ast.SelectorExpr:
			if typ.Sel.Name == "Result" && isExecQual(typ.X) {
				return true
			}
		}
	}
	return false
}
