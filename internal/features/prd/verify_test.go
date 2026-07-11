package prd_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"springfield/internal/features/prd"
)

// TestVerifyOverrideJSONRoundTrip pins that a per-plan {command, enabled}
// override marshals and unmarshals verbatim on the PRD envelope, so an authored
// override reaches the runner via prd.ParseFile intact.
func TestVerifyOverrideJSONRoundTrip(t *testing.T) {
	enabled := false
	in := prd.PRD{
		ID:    "01-scaffold",
		Title: "Initial scaffold",
		Verify: &prd.VerifyOverride{
			Command: "make check",
			Enabled: &enabled,
		},
	}

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out prd.PRD
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if out.Verify == nil {
		t.Fatal("Verify override dropped on round-trip")
	}
	if out.Verify.Command != "make check" {
		t.Errorf("Command = %q, want %q", out.Verify.Command, "make check")
	}
	if out.Verify.Enabled == nil {
		t.Fatal("Enabled pointer dropped; want explicit false")
	}
	if *out.Verify.Enabled != false {
		t.Errorf("Enabled = %v, want false", *out.Verify.Enabled)
	}
}

// TestVerifyOverrideOmittedWhenNil pins that a plan without a verify override
// emits no "verify" key (omitempty), leaving it nil on decode so ResolveVerify
// inherits the global block.
func TestVerifyOverrideOmittedWhenNil(t *testing.T) {
	raw, err := json.Marshal(prd.PRD{ID: "01-scaffold", Title: "x"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "verify") {
		t.Errorf("nil override should be omitted, got %s", raw)
	}

	var out prd.PRD
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Verify != nil {
		t.Errorf("Verify = %+v, want nil", out.Verify)
	}
}

// TestParseEnvelopeWithVerifyOverride pins that the override decodes through the
// public strict envelope parser (unknown fields rejected), confirming "verify"
// is a recognized per-plan key on the envelope.
func TestParseEnvelopeWithVerifyOverride(t *testing.T) {
	body := `{
		"title": "batch",
		"source": "plan",
		"phases": [{"mode": "serial", "plans": ["01-scaffold"]}],
		"plans": [
			{
				"id": "01-scaffold",
				"title": "Initial scaffold",
				"description": "Build it.",
				"context_md": "",
				"tags": [],
				"verify": {"command": "go test ./...", "enabled": true},
				"user_stories": [
					{
						"id": "US-001",
						"title": "t",
						"description": "d",
						"acceptance_criteria": ["ac"],
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
	env, err := prd.ParseEnvelope(strings.NewReader(body))
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	got := env.Plans[0].Verify
	if got == nil {
		t.Fatal("verify override not parsed from envelope")
	}
	if got.Command != "go test ./..." {
		t.Errorf("Command = %q, want %q", got.Command, "go test ./...")
	}
	if got.Enabled == nil || *got.Enabled != true {
		t.Errorf("Enabled = %v, want true", got.Enabled)
	}
}
