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

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
