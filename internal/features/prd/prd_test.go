package prd_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/prd"
)

// validEnvelope returns a minimal valid envelope JSON.
func validEnvelopeJSON() string {
	return `{
		"title": "post-#41 dogfood batch",
		"source": "## My Plan\n\nDo the thing.",
		"phases": [
			{"mode": "serial", "plans": ["01-scaffold"]}
		],
		"plans": [
			{
				"id": "01-scaffold",
				"title": "Initial scaffold",
				"description": "Build the thing.",
				"context_md": "",
				"tags": ["scaffold"],
				"user_stories": [
					{
						"id": "US-001",
						"title": "Bun project init",
						"description": "Init the project.",
						"acceptance_criteria": ["package.json present"],
						"priority": 1,
						"passes": false,
						"deps": [],
						"notes": "",
						"evidence_path": ""
					}
				]
			}
		]
	}`
}

// TestParseEnvelopeValid tests that a well-formed envelope round-trips cleanly.
func TestParseEnvelopeValid(t *testing.T) {
	r := strings.NewReader(validEnvelopeJSON())
	env, err := prd.ParseEnvelope(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Title != "post-#41 dogfood batch" {
		t.Errorf("title = %q, want %q", env.Title, "post-#41 dogfood batch")
	}
	if len(env.Plans) != 1 {
		t.Fatalf("plans len = %d, want 1", len(env.Plans))
	}
	if env.Plans[0].ID != "01-scaffold" {
		t.Errorf("plan id = %q, want %q", env.Plans[0].ID, "01-scaffold")
	}
	if len(env.Plans[0].UserStories) != 1 {
		t.Fatalf("user_stories len = %d, want 1", len(env.Plans[0].UserStories))
	}
	us := env.Plans[0].UserStories[0]
	if us.ID != "US-001" {
		t.Errorf("story id = %q, want %q", us.ID, "US-001")
	}
}

// TestValidateClean ensures a valid envelope produces no errors or warnings.
func TestValidateClean(t *testing.T) {
	r := strings.NewReader(validEnvelopeJSON())
	env, err := prd.ParseEnvelope(r)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result := prd.Validate(env)
	if result.HasErrors() {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", result.Warnings)
	}
}

// TestParseMissingTitle validates envelope-level error for empty title.
func TestParseMissingTitle(t *testing.T) {
	raw := `{
		"title": "",
		"source": "some source",
		"phases": [{"mode": "serial", "plans": ["01-scaffold"]}],
		"plans": [
			{
				"id": "01-scaffold",
				"title": "Scaffold",
				"description": "desc",
				"user_stories": [
					{"id": "US-001", "title": "Story", "description": "d", "acceptance_criteria": ["ok"], "priority": 1, "passes": false, "deps": [], "notes": "", "evidence_path": ""}
				]
			}
		]
	}`
	env, err := prd.ParseEnvelope(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result := prd.Validate(env)
	if !result.HasErrors() {
		t.Fatal("expected errors for missing title, got none")
	}
}

// TestValidateMissingSource validates envelope-level error for empty source.
func TestValidateMissingSource(t *testing.T) {
	r := strings.NewReader(validEnvelopeJSON())
	env, err := prd.ParseEnvelope(r)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	env.Source = ""
	result := prd.Validate(env)
	if !result.HasErrors() {
		t.Fatal("expected errors for missing source, got none")
	}
}

// TestParseUnknownField ensures DisallowUnknownFields rejects unknown keys.
func TestParseUnknownField(t *testing.T) {
	raw := `{
		"title": "t",
		"source": "s",
		"unknown_field": "bad",
		"phases": [],
		"plans": []
	}`
	_, err := prd.ParseEnvelope(strings.NewReader(raw))
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

// TestValidateBadPriority ensures priority=0 produces an error.
func TestValidateBadPriority(t *testing.T) {
	raw := `{
		"title": "t",
		"source": "s",
		"phases": [{"mode": "serial", "plans": ["p1"]}],
		"plans": [
			{
				"id": "p1",
				"title": "Plan 1",
				"description": "d",
				"user_stories": [
					{"id": "US-001", "title": "Story", "description": "d", "acceptance_criteria": ["ok"], "priority": 0, "passes": false, "deps": [], "notes": "", "evidence_path": ""}
				]
			}
		]
	}`
	env, err := prd.ParseEnvelope(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result := prd.Validate(env)
	if !result.HasErrors() {
		t.Fatal("expected error for priority 0, got none")
	}
}

// TestValidateEmptyAcceptanceCriteria ensures empty criteria is a hard error.
func TestValidateEmptyAcceptanceCriteria(t *testing.T) {
	raw := `{
		"title": "t",
		"source": "s",
		"phases": [{"mode": "serial", "plans": ["p1"]}],
		"plans": [
			{
				"id": "p1",
				"title": "Plan 1",
				"description": "d",
				"user_stories": [
					{"id": "US-001", "title": "Story", "description": "d", "acceptance_criteria": [], "priority": 1, "passes": false, "deps": [], "notes": "", "evidence_path": ""}
				]
			}
		]
	}`
	env, err := prd.ParseEnvelope(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result := prd.Validate(env)
	if !result.HasErrors() {
		t.Fatal("expected error for empty acceptance_criteria, got none")
	}
}

// TestValidateDanglingDep ensures deps referencing nonexistent story IDs produce an error.
func TestValidateDanglingDep(t *testing.T) {
	raw := `{
		"title": "t",
		"source": "s",
		"phases": [{"mode": "serial", "plans": ["p1"]}],
		"plans": [
			{
				"id": "p1",
				"title": "Plan 1",
				"description": "d",
				"user_stories": [
					{"id": "US-001", "title": "Story", "description": "d", "acceptance_criteria": ["ok"], "priority": 1, "passes": false, "deps": ["US-999"], "notes": "", "evidence_path": ""}
				]
			}
		]
	}`
	env, err := prd.ParseEnvelope(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result := prd.Validate(env)
	if !result.HasErrors() {
		t.Fatal("expected error for dangling dep, got none")
	}
}

// TestValidateDuplicatePlanID ensures duplicate plan IDs produce an error.
func TestValidateDuplicatePlanID(t *testing.T) {
	raw := `{
		"title": "t",
		"source": "s",
		"phases": [{"mode": "serial", "plans": ["p1"]}],
		"plans": [
			{
				"id": "p1",
				"title": "Plan 1",
				"description": "d",
				"user_stories": [
					{"id": "US-001", "title": "Story", "description": "d", "acceptance_criteria": ["ok"], "priority": 1, "passes": false, "deps": [], "notes": "", "evidence_path": ""}
				]
			},
			{
				"id": "p1",
				"title": "Plan 1 duplicate",
				"description": "d",
				"user_stories": [
					{"id": "US-002", "title": "Story 2", "description": "d", "acceptance_criteria": ["ok"], "priority": 1, "passes": false, "deps": [], "notes": "", "evidence_path": ""}
				]
			}
		]
	}`
	env, err := prd.ParseEnvelope(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result := prd.Validate(env)
	if !result.HasErrors() {
		t.Fatal("expected error for duplicate plan id, got none")
	}
}

// TestValidateDuplicateStoryID ensures duplicate story IDs within a plan produce an error.
func TestValidateDuplicateStoryID(t *testing.T) {
	raw := `{
		"title": "t",
		"source": "s",
		"phases": [{"mode": "serial", "plans": ["p1"]}],
		"plans": [
			{
				"id": "p1",
				"title": "Plan 1",
				"description": "d",
				"user_stories": [
					{"id": "US-001", "title": "Story", "description": "d", "acceptance_criteria": ["ok"], "priority": 1, "passes": false, "deps": [], "notes": "", "evidence_path": ""},
					{"id": "US-001", "title": "Story dup", "description": "d", "acceptance_criteria": ["ok"], "priority": 2, "passes": false, "deps": [], "notes": "", "evidence_path": ""}
				]
			}
		]
	}`
	env, err := prd.ParseEnvelope(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result := prd.Validate(env)
	if !result.HasErrors() {
		t.Fatal("expected error for duplicate story id, got none")
	}
}

// TestValidateCrossPlanDep ensures deps referencing story IDs in a different plan produce an error.
func TestValidateCrossPlanDep(t *testing.T) {
	raw := `{
		"title": "t",
		"source": "s",
		"phases": [{"mode": "serial", "plans": ["p1", "p2"]}],
		"plans": [
			{
				"id": "p1",
				"title": "Plan 1",
				"description": "d",
				"user_stories": [
					{"id": "US-001", "title": "Story", "description": "d", "acceptance_criteria": ["ok"], "priority": 1, "passes": false, "deps": [], "notes": "", "evidence_path": ""}
				]
			},
			{
				"id": "p2",
				"title": "Plan 2",
				"description": "d",
				"user_stories": [
					{"id": "US-002", "title": "Story 2", "description": "d", "acceptance_criteria": ["ok"], "priority": 1, "passes": false, "deps": ["US-001"], "notes": "", "evidence_path": ""}
				]
			}
		]
	}`
	env, err := prd.ParseEnvelope(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result := prd.Validate(env)
	if !result.HasErrors() {
		t.Fatal("expected error for cross-plan dep, got none")
	}
}

// TestValidateMissingContextMD ensures absent context_md causes no error or warning.
func TestValidateMissingContextMD(t *testing.T) {
	// context_md omitted entirely
	raw := `{
		"title": "t",
		"source": "s",
		"phases": [{"mode": "serial", "plans": ["p1"]}],
		"plans": [
			{
				"id": "p1",
				"title": "Plan 1",
				"description": "d",
				"user_stories": [
					{"id": "US-001", "title": "Story", "description": "d", "acceptance_criteria": ["ok"], "priority": 1, "passes": false, "deps": [], "notes": "", "evidence_path": ""}
				]
			}
		]
	}`
	env, err := prd.ParseEnvelope(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result := prd.Validate(env)
	if result.HasErrors() {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", result.Warnings)
	}
}

// TestValidateContextMDWarn ensures 33 KB context_md produces a warning but no error.
func TestValidateContextMDWarn(t *testing.T) {
	bigContext := strings.Repeat("a", 33*1024)
	env := prd.BatchPRDEnvelope{
		Title:  "t",
		Source: "s",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"p1"}}},
		Plans: []prd.BatchPRDPlan{
			{
				PRD: prd.PRD{
					ID:    "p1",
					Title: "Plan 1",
					UserStories: []prd.UserStory{
						{ID: "US-001", Title: "S", AcceptanceCriteria: []string{"ok"}, Priority: 1},
					},
				},
				ContextMD: bigContext,
			},
		},
	}
	result := prd.Validate(env)
	if result.HasErrors() {
		t.Errorf("unexpected errors for 33KB context: %v", result.Errors)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warning for 33KB context_md, got none")
	}
}

// TestValidateContextMDReject ensures 257 KB context_md produces a hard error.
func TestValidateContextMDReject(t *testing.T) {
	hugeContext := strings.Repeat("a", 257*1024)
	env := prd.BatchPRDEnvelope{
		Title:  "t",
		Source: "s",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"p1"}}},
		Plans: []prd.BatchPRDPlan{
			{
				PRD: prd.PRD{
					ID:    "p1",
					Title: "Plan 1",
					UserStories: []prd.UserStory{
						{ID: "US-001", Title: "S", AcceptanceCriteria: []string{"ok"}, Priority: 1},
					},
				},
				ContextMD: hugeContext,
			},
		},
	}
	result := prd.Validate(env)
	if !result.HasErrors() {
		t.Fatal("expected error for 257KB context_md, got none")
	}
}

// TestValidatePhaseReferencesUnknownPlan ensures phases referencing unknown plan IDs produce an error.
func TestValidatePhaseReferencesUnknownPlan(t *testing.T) {
	raw := `{
		"title": "t",
		"source": "s",
		"phases": [{"mode": "serial", "plans": ["p1", "nonexistent"]}],
		"plans": [
			{
				"id": "p1",
				"title": "Plan 1",
				"description": "d",
				"user_stories": [
					{"id": "US-001", "title": "Story", "description": "d", "acceptance_criteria": ["ok"], "priority": 1, "passes": false, "deps": [], "notes": "", "evidence_path": ""}
				]
			}
		]
	}`
	env, err := prd.ParseEnvelope(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result := prd.Validate(env)
	if !result.HasErrors() {
		t.Fatal("expected error for phase referencing unknown plan, got none")
	}
}

// TestValidateZeroStoryPlanIsValid ensures a plan with no user stories is valid.
// The runtime treats zero-story plans as immediately complete (NextStory returns
// PickAllPassed) and sets MergePending without invoking any agent.
func TestValidateZeroStoryPlanIsValid(t *testing.T) {
	raw := `{
		"title": "t",
		"source": "s",
		"phases": [{"mode": "serial", "plans": ["p1"]}],
		"plans": [
			{
				"id": "p1",
				"title": "Plan 1",
				"description": "d",
				"user_stories": []
			}
		]
	}`
	env, err := prd.ParseEnvelope(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result := prd.Validate(env)
	if result.HasErrors() {
		t.Errorf("zero-story plan must be valid, got errors: %v", result.Errors)
	}
}

// TestValidateNonConformingStoryIDIsError ensures non-conforming story ID produces a hard error,
// not just a warning. Non-US IDs cannot be marked via <story-pass>US-(\d+)</story-pass>.
func TestValidateNonConformingStoryIDIsError(t *testing.T) {
	raw := `{
		"title": "t",
		"source": "s",
		"phases": [{"mode": "serial", "plans": ["p1"]}],
		"plans": [
			{
				"id": "p1",
				"title": "Plan 1",
				"description": "d",
				"user_stories": [
					{"id": "STORY-1", "title": "Story", "description": "d", "acceptance_criteria": ["ok"], "priority": 1, "passes": false, "deps": [], "notes": "", "evidence_path": ""}
				]
			}
		]
	}`
	env, err := prd.ParseEnvelope(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result := prd.Validate(env)
	if !result.HasErrors() {
		t.Error("expected error for non-conforming story id (cannot be marked by scanner), got none")
	}
}

// TestValidateBadPlanID ensures a plan ID violating ^[a-z0-9][a-z0-9-]*$ produces an error.
func TestValidateBadPlanID(t *testing.T) {
	raw := `{
		"title": "t",
		"source": "s",
		"phases": [{"mode": "serial", "plans": ["My Plan"]}],
		"plans": [
			{
				"id": "My Plan",
				"title": "Plan 1",
				"description": "d",
				"user_stories": [
					{"id": "US-001", "title": "Story", "description": "d", "acceptance_criteria": ["ok"], "priority": 1, "passes": false, "deps": [], "notes": "", "evidence_path": ""}
				]
			}
		]
	}`
	env, err := prd.ParseEnvelope(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result := prd.Validate(env)
	if !result.HasErrors() {
		t.Fatal("expected errors for invalid plan id, got none")
	}
}

// TestParseFile parses a per-plan prd.json file from disk.
func TestParseFile(t *testing.T) {
	plan := prd.PRD{
		ID:          "01-scaffold",
		Title:       "Initial scaffold",
		Description: "Build the thing.",
		Tags:        []string{"scaffold"},
		UserStories: []prd.UserStory{
			{
				ID:                 "US-001",
				Title:              "Bun project init",
				Description:        "Init the project.",
				AcceptanceCriteria: []string{"package.json present"},
				Priority:           1,
				Passes:             false,
				Deps:               []string{},
				Notes:              "",
				EvidencePath:       "",
			},
		},
	}

	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "prd.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := prd.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if got.ID != "01-scaffold" {
		t.Errorf("id = %q, want %q", got.ID, "01-scaffold")
	}
	if len(got.UserStories) != 1 {
		t.Errorf("user_stories len = %d, want 1", len(got.UserStories))
	}
}

// TestValidationResultHelpers ensures HasErrors and HasWarnings work correctly.
func TestValidationResultHelpers(t *testing.T) {
	empty := prd.ValidationResult{}
	if empty.HasErrors() {
		t.Error("empty result should not have errors")
	}
	if empty.HasWarnings() {
		t.Error("empty result should not have warnings")
	}

	withErr := prd.ValidationResult{Errors: []error{fmt.Errorf("bad")}}
	if !withErr.HasErrors() {
		t.Error("result with error should HasErrors")
	}

	withWarn := prd.ValidationResult{Warnings: []string{"watch out"}}
	if !withWarn.HasWarnings() {
		t.Error("result with warning should HasWarnings")
	}
}
