package planrun_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/conductor/planrun"
	"springfield/internal/features/prd"
	"springfield/internal/features/verify"
)

func samplePRD() prd.PRD {
	return prd.PRD{
		ID:          "test-plan",
		Title:       "Test Plan",
		Description: "A sample plan for testing.",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "First Story", Description: "Do X", AcceptanceCriteria: []string{"X is done"}, Priority: 1, Passes: false},
			{ID: "US-002", Title: "Second Story", Passes: false},
		},
	}
}

func sampleStory() prd.UserStory {
	return prd.UserStory{
		ID:                 "US-001",
		Title:              "First Story",
		Description:        "Do X",
		AcceptanceCriteria: []string{"X is done", "Tests pass"},
		Notes:              "Be careful",
	}
}

func TestBuildPromptEmbeddedTemplatesRenderCorrectly(t *testing.T) {
	dir := t.TempDir()
	p := samplePRD()
	story := sampleStory()

	prompt, err := planrun.BuildPromptForPlan(p, "", "", story, dir)
	if err != nil {
		t.Fatalf("BuildPromptForPlan: %v", err)
	}
	if !strings.Contains(prompt, "test-plan") {
		t.Errorf("prompt missing plan ID")
	}
	if !strings.Contains(prompt, "Test Plan") {
		t.Errorf("prompt missing plan title")
	}
	if !strings.Contains(prompt, "US-001") {
		t.Errorf("prompt missing story ID")
	}
	if !strings.Contains(prompt, "X is done") {
		t.Errorf("prompt missing acceptance criteria")
	}
	if !strings.Contains(prompt, "<promise>COMPLETE</promise>") {
		t.Errorf("prompt missing COMPLETE contract")
	}
}

func TestBuildPromptNoOverrideUsesEmbedded(t *testing.T) {
	dir := t.TempDir()
	// No override files exist — embedded templates must be used.
	prompt, err := planrun.BuildPromptForPlan(samplePRD(), "", "", sampleStory(), dir)
	if err != nil {
		t.Fatalf("BuildPromptForPlan: %v", err)
	}
	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
}

func TestBuildPromptHeaderOverrideOnly(t *testing.T) {
	dir := t.TempDir()
	overrideDir := filepath.Join(dir, "springfield", "prompts")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	headerOverride := "CUSTOM HEADER {{.PlanID}}"
	if err := os.WriteFile(filepath.Join(overrideDir, "header.tmpl"), []byte(headerOverride), 0o644); err != nil {
		t.Fatalf("write header: %v", err)
	}

	prompt, err := planrun.BuildPromptForPlan(samplePRD(), "", "", sampleStory(), dir)
	if err != nil {
		t.Fatalf("BuildPromptForPlan: %v", err)
	}
	if !strings.Contains(prompt, "CUSTOM HEADER test-plan") {
		t.Errorf("operator header not used, prompt: %q", prompt[:min(200, len(prompt))])
	}
	// Footer should still be the embedded version (has COMPLETE contract)
	if !strings.Contains(prompt, "<promise>COMPLETE</promise>") {
		t.Errorf("embedded footer not used")
	}
}

func TestBuildPromptFooterOverrideOnly(t *testing.T) {
	dir := t.TempDir()
	overrideDir := filepath.Join(dir, "springfield", "prompts")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	footerOverride := "CUSTOM FOOTER {{.PlanID}}"
	if err := os.WriteFile(filepath.Join(overrideDir, "footer.tmpl"), []byte(footerOverride), 0o644); err != nil {
		t.Fatalf("write footer: %v", err)
	}

	prompt, err := planrun.BuildPromptForPlan(samplePRD(), "", "", sampleStory(), dir)
	if err != nil {
		t.Fatalf("BuildPromptForPlan: %v", err)
	}
	if !strings.Contains(prompt, "CUSTOM FOOTER test-plan") {
		t.Errorf("operator footer not used")
	}
	// Header should be the embedded version (has plan title)
	if !strings.Contains(prompt, "Test Plan") {
		t.Errorf("embedded header not used")
	}
}

func TestBuildPromptBothOverrides(t *testing.T) {
	dir := t.TempDir()
	overrideDir := filepath.Join(dir, "springfield", "prompts")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(overrideDir, "header.tmpl"), []byte("H {{.PlanID}}"), 0o644); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if err := os.WriteFile(filepath.Join(overrideDir, "footer.tmpl"), []byte("F {{.StoryID}}"), 0o644); err != nil {
		t.Fatalf("write footer: %v", err)
	}

	prompt, err := planrun.BuildPromptForPlan(samplePRD(), "", "", sampleStory(), dir)
	if err != nil {
		t.Fatalf("BuildPromptForPlan: %v", err)
	}
	if !strings.Contains(prompt, "H test-plan") {
		t.Errorf("operator header not used")
	}
	if !strings.Contains(prompt, "F US-001") {
		t.Errorf("operator footer not used")
	}
}

func TestBuildPromptMalformedOverrideLoudError(t *testing.T) {
	dir := t.TempDir()
	overrideDir := filepath.Join(dir, "springfield", "prompts")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(overrideDir, "header.tmpl"), []byte("{{bad template"), 0o644); err != nil {
		t.Fatalf("write bad header: %v", err)
	}

	_, err := planrun.BuildPromptForPlan(samplePRD(), "", "", sampleStory(), dir)
	if err == nil {
		t.Fatal("expected error for malformed override template")
	}
	if !strings.Contains(err.Error(), "header.tmpl") {
		t.Errorf("error should mention the template path, got: %v", err)
	}
}

func TestBuildPromptContextMDSection(t *testing.T) {
	dir := t.TempDir()
	prompt, err := planrun.BuildPromptForPlan(samplePRD(), "some context text", "", sampleStory(), dir)
	if err != nil {
		t.Fatalf("BuildPromptForPlan: %v", err)
	}
	if !strings.Contains(prompt, "# Plan context") {
		t.Errorf("missing Plan context section header")
	}
	if !strings.Contains(prompt, "some context text") {
		t.Errorf("missing context text")
	}
}

func TestBuildPromptContextMDOmittedWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	prompt, err := planrun.BuildPromptForPlan(samplePRD(), "", "", sampleStory(), dir)
	if err != nil {
		t.Fatalf("BuildPromptForPlan: %v", err)
	}
	if strings.Contains(prompt, "# Plan context") {
		t.Errorf("plan context section should be omitted when contextMD is empty")
	}
}

func TestBuildPromptProjectGuidanceSection(t *testing.T) {
	dir := t.TempDir()
	prompt, err := planrun.BuildPromptForPlan(samplePRD(), "", "project guidance text", sampleStory(), dir)
	if err != nil {
		t.Fatalf("BuildPromptForPlan: %v", err)
	}
	if !strings.Contains(prompt, "# Project guidance") {
		t.Errorf("missing Project guidance section header")
	}
	if !strings.Contains(prompt, "project guidance text") {
		t.Errorf("missing guidance text")
	}
}

func TestBuildReviewFixPromptEmbedsFindingsAndCompleteMarker(t *testing.T) {
	plan := prd.PRD{ID: "PLAN-9", Title: "T", Description: "D"}
	got, err := planrun.BuildReviewFixPrompt(plan, "", "", "FINDING: missing nil check.", "")
	if err != nil {
		t.Fatalf("BuildReviewFixPrompt: %v", err)
	}
	for _, want := range []string{"FINDING: missing nil check.", "<promise>COMPLETE</promise>", "PLAN-9"} {
		if !strings.Contains(got, want) {
			t.Fatalf("review-fix prompt missing %q\n---\n%s", want, got)
		}
	}
}

func TestBuildVerifyFixPromptEmbedsCommandExitCodeOutputAndNoWeakenRule(t *testing.T) {
	plan := prd.PRD{
		ID:    "PLAN-7",
		Title: "T",
		UserStories: []prd.UserStory{
			{ID: "US-1", AcceptanceCriteria: []string{"widget compiles cleanly"}},
		},
	}
	req := verify.Request{Command: "go test ./..."}
	res := verify.Result{
		ExitCode: 2,
		Stdout:   "ok pkg/a\n",
		Stderr:   "--- FAIL: TestWidget\n    widget_test.go:42: boom\nFAIL\n",
	}
	got, err := planrun.BuildVerifyFixPrompt(plan, "", "", "", req, res)
	if err != nil {
		t.Fatalf("BuildVerifyFixPrompt: %v", err)
	}
	for _, want := range []string{
		"go test ./...",               // the command
		"2",                           // the exit code
		"widget_test.go:42: boom",     // the failure output tail
		"widget compiles cleanly",     // acceptance criteria
		"<promise>COMPLETE</promise>", // re-emit marker
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("verify-fix prompt missing %q\n---\n%s", want, got)
		}
	}
	// The gate is only meaningful if the fix agent cannot cheat by gutting the
	// suite; the prompt must forbid weakening or removing tests.
	lower := strings.ToLower(got)
	if !strings.Contains(lower, "weaken") || !strings.Contains(lower, "test") {
		t.Fatalf("verify-fix prompt missing no-test-weakening instruction\n---\n%s", got)
	}
}

func TestBuildVerifyFixPromptTailTruncatesHugeOutput(t *testing.T) {
	plan := prd.PRD{ID: "PLAN-8", Title: "T"}
	// A stdout far larger than any sane prompt budget: the builder must keep the
	// actionable tail, not blow the agent's context with the whole transcript.
	huge := strings.Repeat("x", 4<<20) + "FINAL_FAILURE_LINE"
	req := verify.Request{Command: "make verify"}
	res := verify.Result{ExitCode: 1, Stdout: huge}
	got, err := planrun.BuildVerifyFixPrompt(plan, "", "", "", req, res)
	if err != nil {
		t.Fatalf("BuildVerifyFixPrompt: %v", err)
	}
	if !strings.Contains(got, "FINAL_FAILURE_LINE") {
		t.Fatalf("verify-fix prompt dropped the failure tail")
	}
	if len(got) > len(huge) {
		t.Fatalf("verify-fix prompt embedded the whole %d-byte transcript (len=%d); expected truncation", len(huge), len(got))
	}
}

func TestBuildVerifyFixPromptNotesTimeout(t *testing.T) {
	plan := prd.PRD{ID: "PLAN-9", Title: "T"}
	req := verify.Request{Command: "go test ./..."}
	res := verify.Result{ExitCode: -1, TimedOut: true}
	got, err := planrun.BuildVerifyFixPrompt(plan, "", "", "", req, res)
	if err != nil {
		t.Fatalf("BuildVerifyFixPrompt: %v", err)
	}
	if !strings.Contains(strings.ToLower(got), "timed out") {
		t.Fatalf("verify-fix prompt should surface the timeout\n---\n%s", got)
	}
}
