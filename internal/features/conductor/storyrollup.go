package conductor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"springfield/internal/features/prd"
)

// StoryRollup holds per-plan story pass counts loaded from prd.json.
// When Passed == -1 and Total == -1, the prd.json could not be read;
// LoadError describes why. Zero values (Passed==0, Total==0, LoadError=="")
// mean the PRD was loaded and contains no stories (unlikely but valid).
type StoryRollup struct {
	Passed    int
	Total     int
	LoadError string // non-empty when prd.json is missing or malformed
}

// LoadStoryRollups reads prd.json for each unit and returns per-plan story
// counts. IO is isolated here; BuildRegistryStatus stays IO-free.
// Missing or malformed prd.json is recorded in LoadError; it never panics.
func LoadStoryRollups(units []PlanUnit, controlRoot string) map[string]StoryRollup {
	out := make(map[string]StoryRollup, len(units))
	for _, u := range units {
		out[u.ID] = loadRollupForUnit(u, controlRoot)
	}
	return out
}

func loadRollupForUnit(u PlanUnit, controlRoot string) StoryRollup {
	absPath := filepath.Join(controlRoot, u.Path)
	plan, err := prd.ParseFile(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StoryRollup{Passed: -1, Total: -1, LoadError: "prd missing"}
		}
		msg := err.Error()
		// Truncate very long parse errors to keep the status line readable.
		if len(msg) > 120 {
			msg = msg[:120] + "..."
		}
		return StoryRollup{Passed: -1, Total: -1, LoadError: fmt.Sprintf("prd parse error: %s", msg)}
	}

	passed, total := 0, 0
	for _, s := range plan.UserStories {
		total++
		if s.Passes {
			passed++
		}
	}
	return StoryRollup{Passed: passed, Total: total}
}
