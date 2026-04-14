package planner

// Mode identifies whether the planner should ask a follow-up question or return a draft.
type Mode string

const (
	ModeQuestion Mode = "question"
	ModeDraft    Mode = "draft"
)

// Split identifies whether the approved draft should produce one or many workstreams.
type Split string

const (
	SplitSingle Split = "single"
	SplitMulti  Split = "multi"
)

// Response is the narrow structured output contract the planner must satisfy.
type Response struct {
	Mode        Mode         `json:"mode"`
	Question    string       `json:"question,omitempty"`
	WorkID      string       `json:"work_id,omitempty"`
	Title       string       `json:"title,omitempty"`
	Summary     string       `json:"summary,omitempty"`
	Split       Split        `json:"split,omitempty"`
	Workstreams []Workstream `json:"workstreams,omitempty"`
}

// Workstream describes one unit of approved work in a draft response.
type Workstream struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
}

// Conversation is the narrow planning conversation boundary.
type Conversation interface {
	Next(input string) (Response, error)
}

// WorkstreamSummary is the package-safe projection of a planned workstream.
type WorkstreamSummary struct {
	Name    string
	Title   string
	Summary string
}

// Draft is the package-safe review model for one planner-produced draft.
type Draft struct {
	WorkID      string
	Title       string
	Summary     string
	Split       Split
	Workstreams []WorkstreamSummary
}

// PlanResult describes the current planner outcome.
type PlanResult struct {
	Question string
	Draft    *Draft
}
