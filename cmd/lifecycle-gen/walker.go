package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// statusEnumTypeName is the named type whose const declarations define the
// lifecycle's state nodes. The walker errors loudly if the conductor types
// file is missing or contains zero consts of this type.
const statusEnumTypeName = "PlanStatus"

// ExtractStatusNodes parses the Go source file at path and returns one Node
// per declared PlanStatus const, in source order. The Node ID is the const's
// string literal value verbatim; the Label is a title-cased rendering for
// display.
//
// It returns an error when:
//   - the file cannot be read or parsed (e.g. the conductor types file moved);
//   - no PlanStatus consts are found (e.g. the enum was removed or renamed);
//   - a const's RHS is not a single string literal (the codegen contract
//     assumes string-valued enum members).
func ExtractStatusNodes(path string) ([]Node, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("lifecycle-gen: parse %s: %w", path, err)
	}

	var nodes []Node
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		// Within a const block, a spec inherits the type of the prior spec
		// when its own Type is nil (Go const-block semantics). Track the
		// current type so subsequent bare specs can be attributed.
		var currentType string
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if vs.Type != nil {
				if id, ok := vs.Type.(*ast.Ident); ok {
					currentType = id.Name
				} else {
					currentType = ""
				}
			}
			if currentType != statusEnumTypeName {
				continue
			}
			if len(vs.Values) != len(vs.Names) {
				return nil, fmt.Errorf("lifecycle-gen: %s: const spec %v has mismatched names/values", path, vs.Names)
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return nil, fmt.Errorf("lifecycle-gen: %s: %s const value is not a string literal", path, statusEnumTypeName)
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					return nil, fmt.Errorf("lifecycle-gen: %s: unquote %s: %w", path, lit.Value, err)
				}
				nodes = append(nodes, Node{ID: val, Label: titleCase(val)})
			}
		}
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("lifecycle-gen: %s: no %s consts found (enum moved or removed?)", path, statusEnumTypeName)
	}
	return nodes, nil
}

// edgeMarkerPrefix is the in-source token that marks one declared transition.
// The marker is intentionally explicit (not inferred from `Status = StatusX`
// assignments) so the lifecycle stays declarative-in-place — robust to refactors
// of control flow, helper extraction, and assignment-target shape.
const edgeMarkerPrefix = "lifecycle:edge"

// ExtractEdges scans the listed Go source files for `// lifecycle:edge` comment
// markers and returns one Edge per marker, in the order markers appear (paths
// in the slice's order, then by source position within each file).
//
// Marker syntax (single-line `//` comment):
//
//	// lifecycle:edge from=<id> to=<id> kind=<normal|fallback|failure|recovery> [label="<text>"]
//
// `from` and `to` must reference IDs in validNodes (typically the PlanStatus
// const set returned by [ExtractStatusNodes]). Pass nil to skip node validation
// (test-only — production callers should always pin against the live node set).
//
// Resilience: markers are read from each file's CommentGroup list, so they are
// extracted identically regardless of nesting (if/switch/select/for) — there is
// no walk over statement bodies.
//
// Errors:
//   - any file fails to parse;
//   - no markers are found across all files (the source moved or the build is
//     misconfigured — fail loud rather than emit an empty lifecycle);
//   - any marker has bad syntax, an unknown EdgeKind, or references an unknown
//     node ID.
func ExtractEdges(paths []string, validNodes map[string]bool) ([]Edge, error) {
	var out []Edge
	fset := token.NewFileSet()
	for _, p := range paths {
		file, err := parser.ParseFile(fset, p, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("lifecycle-gen: parse %s: %w", p, err)
		}
		for _, group := range file.Comments {
			for _, c := range group.List {
				text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
				if !strings.HasPrefix(text, edgeMarkerPrefix) {
					continue
				}
				edge, err := parseEdgeMarker(strings.TrimSpace(strings.TrimPrefix(text, edgeMarkerPrefix)))
				if err != nil {
					pos := fset.Position(c.Pos())
					return nil, fmt.Errorf("lifecycle-gen: %s:%d: %w", p, pos.Line, err)
				}
				if validNodes != nil {
					if !validNodes[edge.From] {
						pos := fset.Position(c.Pos())
						return nil, fmt.Errorf("lifecycle-gen: %s:%d: 'from' references unknown node %q", p, pos.Line, edge.From)
					}
					if !validNodes[edge.To] {
						pos := fset.Position(c.Pos())
						return nil, fmt.Errorf("lifecycle-gen: %s:%d: 'to' references unknown node %q", p, pos.Line, edge.To)
					}
				}
				out = append(out, edge)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("lifecycle-gen: no `// %s` markers found in %d source file(s) — the marker convention may have moved", edgeMarkerPrefix, len(paths))
	}
	return out, nil
}

// parseEdgeMarker parses a marker payload (everything after `lifecycle:edge`)
// into one Edge. Recognized keys: from, to, kind, label. Unknown keys are an
// error so a typo like `frm=` fails the build instead of silently dropping.
//
// label may be quoted to embed spaces; bare values must be single tokens.
func parseEdgeMarker(payload string) (Edge, error) {
	pairs, err := tokenizeMarker(payload)
	if err != nil {
		return Edge{}, err
	}

	var e Edge
	seen := map[string]bool{}
	for _, kv := range pairs {
		if seen[kv.key] {
			return Edge{}, fmt.Errorf("duplicate key %q in lifecycle:edge marker", kv.key)
		}
		seen[kv.key] = true
		switch kv.key {
		case "from":
			e.From = kv.value
		case "to":
			e.To = kv.value
		case "kind":
			e.Kind = EdgeKind(kv.value)
		case "label":
			e.Label = kv.value
		default:
			return Edge{}, fmt.Errorf("unknown key %q in lifecycle:edge marker (allowed: from, to, kind, label)", kv.key)
		}
	}

	if e.From == "" {
		return Edge{}, fmt.Errorf("lifecycle:edge marker missing required key 'from'")
	}
	if e.To == "" {
		return Edge{}, fmt.Errorf("lifecycle:edge marker missing required key 'to'")
	}
	if !e.Kind.Valid() {
		return Edge{}, fmt.Errorf("lifecycle:edge marker has invalid kind %q (allowed: normal, fallback, failure, recovery)", string(e.Kind))
	}
	return e, nil
}

type kvPair struct{ key, value string }

// tokenizeMarker splits a marker payload into key/value pairs. Bare values are
// terminated at the next whitespace; quoted values (`label="..."`) consume
// everything up to the matching closing quote, allowing embedded spaces.
func tokenizeMarker(s string) ([]kvPair, error) {
	var out []kvPair
	for {
		i := 0
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i == len(s) {
			return out, nil
		}
		s = s[i:]

		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("expected key=value, got %q", s)
		}
		key := s[:eq]
		s = s[eq+1:]
		if len(s) == 0 {
			return nil, fmt.Errorf("key %q has empty value", key)
		}

		var value string
		if s[0] == '"' {
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				return nil, fmt.Errorf("unterminated quoted value for key %q", key)
			}
			value = s[1 : 1+end]
			s = s[1+end+1:]
		} else {
			end := strings.IndexAny(s, " \t")
			if end < 0 {
				value = s
				s = ""
			} else {
				value = s[:end]
				s = s[end:]
			}
		}
		out = append(out, kvPair{key: key, value: value})
	}
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
