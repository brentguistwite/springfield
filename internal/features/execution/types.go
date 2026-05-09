package execution

// Work is the Springfield-owned execution input for one approved work item.
type Work struct {
	ID          string
	Title       string
	RequestBody string
	Split       string
	Workstreams []Workstream
}

// Workstream is one approved Springfield workstream ready for execution.
type Workstream struct {
	Name         string
	Title        string
	Status       string
	Error        string
	EvidencePath string
}

// WorkstreamRun captures one Springfield workstream execution outcome.
type WorkstreamRun struct {
	Name         string
	Status       string
	Error        string
	EvidencePath string
}

// Report is the Springfield-owned execution result from an internal engine.
type Report struct {
	Status      string
	Error       string
	Workstreams []WorkstreamRun
	// AgentID is the adapter id that executed the work, when known (e.g.
	// "claude"). Empty on configuration failures before dispatch.
	AgentID string
	// ExitCode is the OS exit status reported by the agent process.
	ExitCode int
}

// Executor is Springfield's runtime adapter boundary for approved work.
type Executor interface {
	Run(root string, work Work) (Report, error)
}

// SingleExecutor runs one single-stream Springfield work item.
type SingleExecutor interface {
	Run(root string, work Work) (Report, error)
}

// MultiExecutor runs one multi-stream Springfield work item.
type MultiExecutor interface {
	Run(root string, work Work) (Report, error)
}
