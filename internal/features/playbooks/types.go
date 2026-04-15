package playbooks

// Purpose identifies the Springfield-owned playbook contract callers request.
type Purpose string

const (
	PurposePlan    Purpose = "plan"
	PurposeExplain Purpose = "explain"
	PurposeStart   Purpose = "start"
	PurposeStatus  Purpose = "status"
	PurposeRecover Purpose = "recover"
)

// Input is the Springfield-owned prompt build contract.
type Input struct {
	Purpose               Purpose
	ProjectRoot           string
	IncludeProjectContext bool
	TaskBody              string
}

// Output captures the resolved sources plus the rendered prompt.
type Output struct {
	BuiltinSource string
	ProjectSource string
	Prompt        string
}
