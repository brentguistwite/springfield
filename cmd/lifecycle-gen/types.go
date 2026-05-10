// Package main hosts the lifecycle-gen codegen binary.
//
// Types in this file ARE the schema for flowchart/public/lifecycle.json.
// Downstream consumers (the flowchart UI) decode against the JSON tags below,
// so renaming or retagging a field is a wire-format break.
package main

// Lifecycle is the top-level schema written to flowchart/public/lifecycle.json.
type Lifecycle struct {
	// Nodes is the set of states a plan/slice can occupy.
	Nodes []Node `json:"nodes"`
	// Edges is the set of transitions between states, including fallbacks and recoveries.
	Edges []Edge `json:"edges"`
}

// Node is one state in the lifecycle graph.
type Node struct {
	// ID is the stable identifier used by edges to reference this node (e.g. "running").
	ID string `json:"id"`
	// Label is the human-readable display name (e.g. "Running").
	Label string `json:"label"`
}

// Edge is a directed transition from one state to another.
type Edge struct {
	// From is the source node ID.
	From string `json:"from"`
	// To is the destination node ID.
	To string `json:"to"`
	// Kind classifies the transition; see EdgeKind constants.
	Kind EdgeKind `json:"kind"`
	// Label is an optional short description of the trigger (e.g. "agent fallback").
	Label string `json:"label,omitempty"`
}

// EdgeKind classifies a lifecycle transition. The string value is wire-stable.
type EdgeKind string

const (
	// EdgeNormal is a forward-progress transition on the happy path.
	EdgeNormal EdgeKind = "normal"
	// EdgeFallback is an agent-priority switch (e.g. Claude → Codex on rate limit).
	EdgeFallback EdgeKind = "fallback"
	// EdgeFailure is a terminal-failure transition into a failed state.
	EdgeFailure EdgeKind = "failure"
	// EdgeRecovery is a resume-from-failed transition (e.g. springfield recover).
	EdgeRecovery EdgeKind = "recovery"
)

// Valid reports whether k is one of the documented EdgeKind constants.
func (k EdgeKind) Valid() bool {
	switch k {
	case EdgeNormal, EdgeFallback, EdgeFailure, EdgeRecovery:
		return true
	}
	return false
}
