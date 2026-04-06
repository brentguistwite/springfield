package ralph_test

import (
	"encoding/json"
	"testing"

	"springfield/internal/features/ralph"
)

func TestSpecUnmarshalAcceptsPRDFormat(t *testing.T) {
	// PRD files use "userStories", "passes", and "deps" — Spec must accept these.
	raw := `{
		"project": "smoke-test",
		"branchName": "smoke/test",
		"description": "PRD-format plan",
		"userStories": [
			{
				"id": "US-001",
				"title": "First story",
				"description": "A test story",
				"priority": 1,
				"passes": true,
				"deps": ["US-000"]
			},
			{
				"id": "US-002",
				"title": "Second story",
				"passes": false,
				"deps": []
			}
		]
	}`

	var spec ralph.Spec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatalf("unmarshal PRD-format spec: %v", err)
	}

	if len(spec.Stories) != 2 {
		t.Fatalf("expected 2 stories from userStories, got %d", len(spec.Stories))
	}

	s := spec.Stories[0]
	if s.ID != "US-001" {
		t.Fatalf("expected US-001, got %s", s.ID)
	}
	if !s.Passed {
		t.Fatal("expected passes:true to map to Passed=true")
	}
	if len(s.DependsOn) != 1 || s.DependsOn[0] != "US-000" {
		t.Fatalf("expected deps to map to DependsOn, got %v", s.DependsOn)
	}

	if spec.Stories[1].Passed {
		t.Fatal("expected passes:false to map to Passed=false")
	}
}

func TestSpecUnmarshalNativeFormatStillWorks(t *testing.T) {
	raw := `{
		"project": "native",
		"stories": [
			{"id": "US-001", "title": "Native", "passed": true, "dependsOn": ["US-000"]}
		]
	}`

	var spec ralph.Spec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatalf("unmarshal native spec: %v", err)
	}

	if len(spec.Stories) != 1 {
		t.Fatalf("expected 1 story, got %d", len(spec.Stories))
	}
	if !spec.Stories[0].Passed {
		t.Fatal("expected Passed=true from native format")
	}
	if len(spec.Stories[0].DependsOn) != 1 {
		t.Fatalf("expected DependsOn from native format, got %v", spec.Stories[0].DependsOn)
	}
}
