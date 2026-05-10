package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// defaultOutRel is the repo-relative output path. The flowchart UI checks the
// generated JSON into source so changes to the lifecycle are reviewable in PRs.
const defaultOutRel = "flowchart/public/lifecycle.json"

// transitionSources lists the live Go files the walker scans for
// `// lifecycle:edge` markers. Edges appear in this slice order; within each
// file they appear in source position order. Keep this list in sync with the
// test helper transitionSourcePaths in testhelpers_test.go.
var transitionSources = []string{
	"cmd/start.go",
	"cmd/recover.go",
	"internal/features/conductor/recover.go",
	"internal/features/conductor/planrun/runner.go",
	"internal/core/runtime/runner.go",
}

// conductorTypesRel is the repo-relative path to the PlanStatus enum.
const conductorTypesRel = "internal/features/conductor/types.go"

func main() {
	out := flag.String("out", "", "output path (default: "+defaultOutRel+" relative to repo root)")
	flag.Parse()

	root := repoRoot()
	outPath := *out
	if outPath == "" {
		outPath = filepath.Join(root, defaultOutRel)
	}

	if err := generate(root, outPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// generate is the codegen entry point: parse the live conductor types + the
// listed transition source files, assemble the lifecycle graph, and write
// deterministic JSON to outPath (creating parent dirs as needed).
func generate(root, outPath string) error {
	lc, err := Build(
		filepath.Join(root, conductorTypesRel),
		absPaths(root, transitionSources),
	)
	if err != nil {
		return err
	}
	b, err := MarshalDeterministic(lc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("lifecycle-gen: mkdir %s: %w", filepath.Dir(outPath), err)
	}
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		return fmt.Errorf("lifecycle-gen: write %s: %w", outPath, err)
	}
	return nil
}

// Build runs the AST walker against the conductor types file and the listed
// transition source files, returning a populated Lifecycle. The order of nodes
// follows the order of PlanStatus const declarations; the order of edges
// follows transitionPaths order, then source position within each file.
func Build(typesPath string, transitionPaths []string) (Lifecycle, error) {
	nodes, err := ExtractStatusNodes(typesPath)
	if err != nil {
		return Lifecycle{}, err
	}
	valid := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		valid[n.ID] = true
	}
	edges, err := ExtractEdges(transitionPaths, valid)
	if err != nil {
		return Lifecycle{}, err
	}
	return Lifecycle{Nodes: nodes, Edges: edges}, nil
}

// MarshalDeterministic emits the lifecycle as pretty-printed JSON with a
// trailing newline. The walker contract pins node/edge order to source order,
// so byte-output is stable across runs for a fixed source tree.
func MarshalDeterministic(lc Lifecycle) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(lc); err != nil {
		return nil, fmt.Errorf("lifecycle-gen: marshal: %w", err)
	}
	return buf.Bytes(), nil
}

// repoRoot resolves the repo root from this file's compile-time location so
// `go run ./cmd/lifecycle-gen` works from any cwd inside the module.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func absPaths(root string, rels []string) []string {
	out := make([]string, len(rels))
	for i, r := range rels {
		out[i] = filepath.Join(root, r)
	}
	return out
}
