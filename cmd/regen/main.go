// regen regenerates canonical skill and command files from Go definitions.
// Run: go run ./cmd/regen
package main

import (
	"fmt"
	"os"

	"springfield/internal/features/skills"
)

func main() {
	for _, name := range []string{"plan", "start", "status", "recover"} {
		r, err := skills.Render(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render %s: %v\n", name, err)
			os.Exit(1)
		}
		path := "skills/" + name + "/SKILL.md"
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
		if err := os.WriteFile(cmdPath, []byte(rc.Content), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", cmdPath, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s\n", cmdPath)
	}
}
