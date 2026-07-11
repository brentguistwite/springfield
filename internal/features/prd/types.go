// Package prd defines the PRD/UserStory schema used by Springfield to structure
// and validate batch plan envelopes. All types are pure data; no IO is performed here.
package prd

// UserStory is a single testable requirement within a plan.
type UserStory struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Priority           int      `json:"priority"`
	Passes             bool     `json:"passes"`
	Deps               []string `json:"deps"`
	Notes              string   `json:"notes"`
	EvidencePath       string   `json:"evidence_path"`
}

// PRD is the per-plan document written to .springfield/plans/<plan-id>/prd.json.
// It holds the plan's identity, descriptive metadata, and user stories.
type PRD struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Tags        []string    `json:"tags"`
	UserStories []UserStory `json:"user_stories"`

	// Review is the per-plan pre-merge review toggle. Tri-state:
	//   nil   → use the project-global [review].enabled default
	//   true  → force review on for this plan (even if globally disabled)
	//   false → suppress review for this plan (even if globally enabled)
	// Authored in the BatchPRDEnvelope and marshaled verbatim into prd.json by
	// batch.Compile, so it reaches the runner via prd.ParseFile. Resolved with
	// config.ReviewEnabledForPlan.
	Review *bool `json:"review,omitempty"`

	// Verify is the per-plan override for the verify completion gate. nil means
	// "inherit the project-global [verify] block". A non-nil override wins per
	// field: an explicit Enabled flips the gate on/off for this plan even
	// against the global default, and a non-empty Command replaces the global
	// command. Authored in the BatchPRDEnvelope and marshaled verbatim into
	// prd.json, reaching the runner via prd.ParseFile. Resolved with
	// config.ResolveVerify / config.VerifyEnabledForPlan.
	Verify *VerifyOverride `json:"verify,omitempty"`
}

// VerifyOverride is the per-plan {command, enabled} override on the PRD
// envelope for the verify completion gate. It is pure data; the precedence
// rules that fold it onto the global [verify] block live in config.ResolveVerify.
type VerifyOverride struct {
	// Command, when non-empty, replaces the global verify command for this plan.
	// Empty inherits the global command.
	Command string `json:"command,omitempty"`
	// Enabled is tri-state via the pointer:
	//   nil   → inherit the global [verify].enabled default
	//   true  → force the gate on for this plan (even if globally disabled)
	//   false → force the gate off for this plan (even if globally enabled)
	Enabled *bool `json:"enabled,omitempty"`
}

// PhasePRD describes one execution phase inside a BatchPRDEnvelope.
// Mode is an execution hint (e.g. "serial", "parallel").
// Plans lists the plan IDs that belong to this phase.
type PhasePRD struct {
	Mode  string   `json:"mode"`
	Plans []string `json:"plans"`
}

// BatchPRDPlan is an envelope-level plan entry. It embeds PRD and adds
// ContextMD, which provides optional inline context injected into the agent
// prompt but is not stored in the per-plan prd.json on disk.
type BatchPRDPlan struct {
	PRD
	ContextMD string `json:"context_md"`
}

// BatchPRDEnvelope is the top-level document produced by the planner and
// consumed by the conductor. It carries the full set of plans and their phase
// ordering in a single file.
type BatchPRDEnvelope struct {
	Title  string         `json:"title"`
	Source string         `json:"source"`
	Phases []PhasePRD     `json:"phases"`
	Plans  []BatchPRDPlan `json:"plans"`
}

// ValidationResult aggregates all errors and warnings from a Validate call.
// Callers can surface warnings without blocking execution when HasErrors is false.
type ValidationResult struct {
	Errors   []error
	Warnings []string
}

// HasErrors reports whether any hard validation errors were found.
func (r ValidationResult) HasErrors() bool {
	return len(r.Errors) > 0
}

// HasWarnings reports whether any validation warnings were found.
func (r ValidationResult) HasWarnings() bool {
	return len(r.Warnings) > 0
}
