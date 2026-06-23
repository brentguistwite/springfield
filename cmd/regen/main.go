// regen regenerates canonical skill and command files from Go definitions.
// Run: go run ./cmd/regen
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"springfield/internal/features/skills"
)

func main() {
	names := []string{"plan", "plan-from-issue", "status", "recover"}

	known := make(map[string]bool, len(names))
	for _, name := range names {
		known[name] = true

		r, err := skills.Render(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render %s: %v\n", name, err)
			os.Exit(1)
		}
		path := "skills/" + name + "/SKILL.md"
		if err := os.MkdirAll("skills/"+name, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "mkdir skills/%s: %v\n", name, err)
			os.Exit(1)
		}
		if err := os.WriteFile(path, []byte(r.Content), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s\n", path)

		rc, err := skills.RenderCommand(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render command %s: %v\n", name, err)
			os.Exit(1)
		}
		cmdPath := "commands/" + name + ".md"
		if err := os.MkdirAll("commands", 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "mkdir commands: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(cmdPath, []byte(rc.Content), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", cmdPath, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s\n", cmdPath)
	}

	// Prune stale generated artifacts so a rename/removal can't leave an
	// orphaned skill or command behind (the drift guards only check that the
	// CURRENT set matches — they never catch an extra leftover file). skills/
	// and commands/ hold nothing but generated output, so anything not in the
	// catalog set is safe to delete.
	pruneStaleArtifacts(known)
}

// pruneStaleArtifacts removes any skills/<name>/ directory or commands/<name>.md
// file whose <name> is not in the generated set.
func pruneStaleArtifacts(known map[string]bool) {
	if entries, err := os.ReadDir("skills"); err == nil {
		for _, e := range entries {
			if e.IsDir() && !known[e.Name()] {
				dir := filepath.Join("skills", e.Name())
				if err := os.RemoveAll(dir); err != nil {
					fmt.Fprintf(os.Stderr, "prune %s: %v\n", dir, err)
					os.Exit(1)
				}
				fmt.Printf("pruned %s\n", dir)
			}
		}
	}

	if entries, err := os.ReadDir("commands"); err == nil {
		for _, e := range entries {
			name := strings.TrimSuffix(e.Name(), ".md")
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && !known[name] {
				path := filepath.Join("commands", e.Name())
				if err := os.Remove(path); err != nil {
					fmt.Fprintf(os.Stderr, "prune %s: %v\n", path, err)
					os.Exit(1)
				}
				fmt.Printf("pruned %s\n", path)
			}
		}
	}
}
