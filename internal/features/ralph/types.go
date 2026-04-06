package ralph

import (
	"encoding/json"
	"time"
)

// Story captures one Ralph work item.
type Story struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Priority    int      `json:"priority,omitempty"`
	Passed      bool     `json:"passed,omitempty"`
	DependsOn   []string `json:"dependsOn,omitempty"`
}

// storyAlias decodes PRD-format fields ("passes", "deps") alongside native fields.
type storyAlias struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Priority    int      `json:"priority,omitempty"`
	Passed      bool     `json:"passed,omitempty"`
	Passes      bool     `json:"passes,omitempty"`
	DependsOn   []string `json:"dependsOn,omitempty"`
	Deps        []string `json:"deps,omitempty"`
}

// UnmarshalJSON accepts both native ("passed","dependsOn") and PRD ("passes","deps") fields.
func (s *Story) UnmarshalJSON(data []byte) error {
	var a storyAlias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	s.ID = a.ID
	s.Title = a.Title
	s.Description = a.Description
	s.Priority = a.Priority
	s.Passed = a.Passed || a.Passes
	s.DependsOn = a.DependsOn
	if len(s.DependsOn) == 0 && len(a.Deps) > 0 {
		s.DependsOn = a.Deps
	}
	return nil
}

// Spec is the persisted Ralph plan definition.
type Spec struct {
	Project     string  `json:"project"`
	BranchName  string  `json:"branchName,omitempty"`
	Description string  `json:"description,omitempty"`
	Stories     []Story `json:"stories"`
}

// specAlias decodes PRD-format "userStories" alongside native "stories".
type specAlias struct {
	Project     string  `json:"project"`
	BranchName  string  `json:"branchName,omitempty"`
	Description string  `json:"description,omitempty"`
	Stories     []Story `json:"stories"`
	UserStories []Story `json:"userStories,omitempty"`
}

// UnmarshalJSON accepts both "stories" and "userStories" as the story list key.
func (s *Spec) UnmarshalJSON(data []byte) error {
	var a specAlias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	s.Project = a.Project
	s.BranchName = a.BranchName
	s.Description = a.Description
	s.Stories = a.Stories
	if len(s.Stories) == 0 && len(a.UserStories) > 0 {
		s.Stories = a.UserStories
	}
	return nil
}

// Plan is the in-memory Ralph plan boundary.
type Plan struct {
	Name string `json:"name"`
	Spec Spec   `json:"spec"`
}

// RunRecord captures one Ralph execution attempt.
type RunRecord struct {
	ID        string    `json:"id"`
	PlanName  string    `json:"planName"`
	StoryID   string    `json:"storyId"`
	Agent     string    `json:"agent,omitempty"`
	Status    string    `json:"status"`
	ExitCode  int       `json:"exitCode,omitempty"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt"`
}

// RunResult is the structured outcome from a StoryExecutor.
type RunResult struct {
	Agent    string
	ExitCode int
	Err      error
}

// StoryExecutor is the adapter boundary for story execution.
type StoryExecutor interface {
	Execute(Story) RunResult
}
