package playbooks

// Kind identifies the internal engine playbook Springfield should render.
type Kind string

const (
	KindRalph     Kind = "ralph"
	KindConductor Kind = "conductor"
)

// Input is the Springfield-owned prompt build contract.
type Input struct {
	Kind        Kind
	ProjectRoot string
	TaskBody    string
}

// Output captures the resolved sources plus the rendered prompt.
type Output struct {
	BuiltinSource string
	ProjectSource string
	Prompt        string
}
