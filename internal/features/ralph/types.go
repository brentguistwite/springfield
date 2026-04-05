package ralph

import "time"

// Story captures one Ralph work item.
type Story struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Priority    int      `json:"priority,omitempty"`
	Passed      bool     `json:"passed,omitempty"`
	DependsOn   []string `json:"dependsOn,omitempty"`
}

// Spec is the persisted Ralph plan definition.
type Spec struct {
	Project     string  `json:"project"`
	BranchName  string  `json:"branchName,omitempty"`
	Description string  `json:"description,omitempty"`
	Stories     []Story `json:"stories"`
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
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt"`
}

// StoryExecutor is the adapter boundary for story execution.
type StoryExecutor interface {
	Execute(Story) error
}
