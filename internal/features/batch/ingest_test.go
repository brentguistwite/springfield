package batch

import (
	"strings"
	"testing"
)

func TestParseSlicePayload_Valid(t *testing.T) {
	j := `{
	  "title": "add oauth",
	  "source": "# add oauth\n\nplan body",
	  "slices": [
	    {"id":"01","title":"scaffold","summary":"a"},
	    {"id":"02","title":"wire endpoint","summary":"b"}
	  ]
	}`
	p, err := ParseSlicePayload(strings.NewReader(j))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if p.Title != "add oauth" {
		t.Fatalf("title = %q", p.Title)
	}
	if len(p.Slices) != 2 {
		t.Fatalf("slices = %d", len(p.Slices))
	}
	if p.Slices[0].ID != "01" || p.Slices[1].Title != "wire endpoint" {
		t.Fatalf("slice data wrong: %+v", p.Slices)
	}
}

func TestParseSlicePayload_RejectsEmptySlices(t *testing.T) {
	j := `{"title":"x","source":"y","slices":[]}`
	if _, err := ParseSlicePayload(strings.NewReader(j)); err == nil {
		t.Fatal("expected error for empty slices")
	}
}

func TestParseSlicePayload_RejectsDuplicateIDs(t *testing.T) {
	j := `{"title":"x","source":"y","slices":[{"id":"01","title":"a"},{"id":"01","title":"b"}]}`
	if _, err := ParseSlicePayload(strings.NewReader(j)); err == nil {
		t.Fatal("expected error for duplicate ids")
	}
}

func TestParseSlicePayload_RejectsMissingTitle(t *testing.T) {
	j := `{"title":"x","source":"y","slices":[{"id":"01","title":""}]}`
	if _, err := ParseSlicePayload(strings.NewReader(j)); err == nil {
		t.Fatal("expected error for missing slice title")
	}
}

func TestParseSlicePayload_RejectsMissingTopLevelTitle(t *testing.T) {
	j := `{"title":"","source":"y","slices":[{"id":"01","title":"a"}]}`
	if _, err := ParseSlicePayload(strings.NewReader(j)); err == nil {
		t.Fatal("expected error for missing title")
	}
}

func TestParseSlicePayload_RejectsUnknownFields(t *testing.T) {
	j := `{"title":"x","source":"y","slices":[{"id":"01","title":"a"}],"extra":1}`
	if _, err := ParseSlicePayload(strings.NewReader(j)); err == nil {
		t.Fatal("expected error for unknown field")
	}
}
