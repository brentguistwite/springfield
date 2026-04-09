package planner

import (
	"fmt"
	"strings"
)

// Validate enforces the planner response contract before Springfield accepts it.
func Validate(resp Response) error {
	switch resp.Mode {
	case ModeQuestion:
		if strings.TrimSpace(resp.Question) == "" {
			return fmt.Errorf("question mode requires question")
		}
		return nil
	case ModeDraft:
		if strings.TrimSpace(resp.WorkID) == "" {
			return fmt.Errorf("draft mode requires work id")
		}
		if strings.TrimSpace(resp.Title) == "" {
			return fmt.Errorf("draft mode requires title")
		}
		if strings.TrimSpace(resp.Summary) == "" {
			return fmt.Errorf("draft mode requires summary")
		}
		if len(resp.Workstreams) == 0 {
			return fmt.Errorf("draft mode requires at least one workstream")
		}
		for index, workstream := range resp.Workstreams {
			if strings.TrimSpace(workstream.Name) == "" {
				return fmt.Errorf("workstream %d requires name", index)
			}
			if strings.TrimSpace(workstream.Title) == "" {
				return fmt.Errorf("workstream %d requires title", index)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported planner mode %q", resp.Mode)
	}
}
