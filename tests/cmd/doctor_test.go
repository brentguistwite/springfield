package cmd_test

import (
	"strings"
	"testing"
)

func TestDoctorReportsAgentDetection(t *testing.T) {
	bin := buildBinary(t)

	output, err := runBinaryIn(t, bin, t.TempDir(), "doctor")
	if err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, output)
	}

	// Doctor must mention all supported agent binaries
	for _, binary := range []string{"claude", "codex", "gemini"} {
		if !strings.Contains(output, binary) {
			t.Errorf("expected doctor to mention %s, got:\n%s", binary, output)
		}
	}

	// Must contain an agent availability summary
	if !strings.Contains(output, "agent") {
		t.Errorf("expected agent summary in doctor output, got:\n%s", output)
	}
}

func TestDoctorHelpDescribesFeature(t *testing.T) {
	output, err := runSpringfield(t, "doctor", "--help")
	if err != nil {
		t.Fatalf("doctor --help failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "agent CLIs") {
		t.Errorf("expected doctor help to describe agent CLI checks, got:\n%s", output)
	}
}
