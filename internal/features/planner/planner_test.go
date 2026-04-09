package planner_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"springfield/internal/features/planner"
)

type fakeRunner struct {
	prompt  string
	prompts []string
	output  string
	outputs []string
	err     error
}

func (f *fakeRunner) Run(prompt string) (string, error) {
	f.prompt = prompt
	f.prompts = append(f.prompts, prompt)
	if f.err != nil {
		return "", f.err
	}
	if len(f.outputs) > 0 {
		output := f.outputs[0]
		f.outputs = f.outputs[1:]
		return output, nil
	}
	return f.output, nil
}

func TestValidateAcceptsQuestionResponse(t *testing.T) {
	resp := planner.Response{
		Mode:     planner.ModeQuestion,
		Question: "What is the first workflow you want Springfield to plan?",
	}

	if err := planner.Validate(resp); err != nil {
		t.Fatalf("validate question response: %v", err)
	}
}

func TestValidateAcceptsSingleDraft(t *testing.T) {
	resp := planner.Response{
		Mode:    planner.ModeDraft,
		WorkID:  "wave-b",
		Title:   "Wave B planning surface",
		Summary: "Add planner contract, session loop, writer, explain, and TUI review flow.",
		Split:   planner.SplitSingle,
		Workstreams: []planner.Workstream{
			{
				Name:    "01",
				Title:   "Implement Wave B",
				Summary: "Keep the planning surface in one workstream.",
			},
		},
	}

	if err := planner.Validate(resp); err != nil {
		t.Fatalf("validate single draft: %v", err)
	}
}

func TestValidateAcceptsMultiDraft(t *testing.T) {
	resp := planner.Response{
		Mode:    planner.ModeDraft,
		WorkID:  "wave-b",
		Title:   "Wave B planning surface",
		Summary: "Split UI from planner core work.",
		Split:   planner.SplitMulti,
		Workstreams: []planner.Workstream{
			{
				Name:    "01",
				Title:   "Planner core",
				Summary: "Add planner types and session.",
			},
			{
				Name:    "02",
				Title:   "TUI review",
				Summary: "Add Springfield-first review flow.",
			},
		},
	}

	if err := planner.Validate(resp); err != nil {
		t.Fatalf("validate multi draft: %v", err)
	}
}

func TestValidateRejectsEmptyQuestion(t *testing.T) {
	resp := planner.Response{
		Mode: planner.ModeQuestion,
	}

	if err := planner.Validate(resp); err == nil {
		t.Fatal("expected empty question to fail validation")
	}
}

func TestValidateRejectsEmptyWorkID(t *testing.T) {
	resp := planner.Response{
		Mode:    planner.ModeDraft,
		Title:   "Wave B planning surface",
		Summary: "Missing work id should fail.",
		Split:   planner.SplitSingle,
		Workstreams: []planner.Workstream{
			{
				Name:  "01",
				Title: "Planner core",
			},
		},
	}

	if err := planner.Validate(resp); err == nil {
		t.Fatal("expected empty work id to fail validation")
	}
}

func TestValidateRejectsWorkstreamWithoutTitle(t *testing.T) {
	resp := planner.Response{
		Mode:    planner.ModeDraft,
		WorkID:  "wave-b",
		Title:   "Wave B planning surface",
		Summary: "Missing workstream title should fail.",
		Split:   planner.SplitSingle,
		Workstreams: []planner.Workstream{
			{
				Name: "01",
			},
		},
	}

	if err := planner.Validate(resp); err == nil {
		t.Fatal("expected missing workstream title to fail validation")
	}
}

func TestSessionNextBuildsPlanningPromptAndParsesResponse(t *testing.T) {
	root := t.TempDir()
	projectContext := "project instructions from AGENTS"
	if err := os.WriteFile(root+"/AGENTS.md", []byte(projectContext), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	runner := &fakeRunner{
		output: `{
			"mode":"draft",
			"work_id":"wave-b",
			"title":"Wave B planning surface",
			"summary":"Add the planning surface.",
			"split":"single",
			"workstreams":[
				{"name":"01","title":"Implement Wave B","summary":"One workstream."}
			]
		}`,
	}

	session := planner.Session{
		ProjectRoot: root,
		Runner:      runner,
	}

	resp, err := session.Next("Add Wave B planning surface")
	if err != nil {
		t.Fatalf("session next: %v", err)
	}

	if resp.Mode != planner.ModeDraft {
		t.Fatalf("mode = %q", resp.Mode)
	}
	if resp.WorkID != "wave-b" {
		t.Fatalf("work id = %q", resp.WorkID)
	}
	if got, want := len(resp.Workstreams), 1; got != want {
		t.Fatalf("workstreams = %d, want %d", got, want)
	}

	if runner.prompt == "" {
		t.Fatal("expected session to send prompt to runner")
	}
	if !strings.Contains(runner.prompt, projectContext) {
		t.Fatalf("prompt should include AGENTS context, got:\n%s", runner.prompt)
	}
	if !strings.Contains(runner.prompt, "Add Wave B planning surface") {
		t.Fatalf("prompt should include user input, got:\n%s", runner.prompt)
	}
	if !strings.Contains(runner.prompt, "Built-in Conductor playbook.") {
		t.Fatalf("prompt should include built playbook content, got:\n%s", runner.prompt)
	}
	if !strings.Contains(runner.prompt, "JSON") {
		t.Fatalf("prompt should require JSON output, got:\n%s", runner.prompt)
	}
}

func TestSessionNextRejectsInvalidPlannerResponse(t *testing.T) {
	session := planner.Session{
		ProjectRoot: t.TempDir(),
		Runner: &fakeRunner{
			output: `{"mode":"question","question":" "}`,
		},
	}

	if _, err := session.Next("Need next question"); err == nil {
		t.Fatal("expected invalid planner response to fail")
	}
}

func TestSessionNextCarriesInitialRequestIntoFollowUpTurns(t *testing.T) {
	root := t.TempDir()
	projectContext := "project instructions from AGENTS"
	if err := os.WriteFile(root+"/AGENTS.md", []byte(projectContext), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	runner := &fakeRunner{
		outputs: []string{
			`{"mode":"question","question":"Which workflow surface should ship first?"}`,
			`{
				"mode":"draft",
				"work_id":"wave-c1",
				"title":"Wave C1 planning loop",
				"summary":"Connect the TUI planning flow to the real planner session.",
				"split":"single",
				"workstreams":[
					{"name":"01","title":"Implement Wave C1","summary":"Keep it in one stream."}
				]
			}`,
		},
	}

	session := &planner.Session{
		ProjectRoot: root,
		Runner:      runner,
	}

	first, err := session.Next("Connect the TUI planning flow to the real planner session")
	if err != nil {
		t.Fatalf("first session next: %v", err)
	}
	if first.Mode != planner.ModeQuestion {
		t.Fatalf("first mode = %q", first.Mode)
	}

	second, err := session.Next("Start with New Work")
	if err != nil {
		t.Fatalf("second session next: %v", err)
	}
	if second.Mode != planner.ModeDraft {
		t.Fatalf("second mode = %q", second.Mode)
	}

	if got, want := len(runner.prompts), 2; got != want {
		t.Fatalf("prompts = %d, want %d", got, want)
	}

	followUpPrompt := runner.prompts[1]
	for _, want := range []string{
		projectContext,
		"Connect the TUI planning flow to the real planner session",
		"Which workflow surface should ship first?",
		"Start with New Work",
	} {
		if !strings.Contains(followUpPrompt, want) {
			t.Fatalf("follow-up prompt should contain %q, got:\n%s", want, followUpPrompt)
		}
	}
}

func TestSessionNextStillRejectsInvalidPlannerResponseAfterQuestion(t *testing.T) {
	runner := &fakeRunner{
		outputs: []string{
			`{"mode":"question","question":"Which workflow surface should ship first?"}`,
			`{"mode":"draft","work_id":" ","title":"Broken","summary":"still broken","split":"single","workstreams":[{"name":"01","title":"Broken"}]}`,
		},
	}
	session := &planner.Session{
		ProjectRoot: t.TempDir(),
		Runner:      runner,
	}

	first, err := session.Next("Connect the planner")
	if err != nil {
		t.Fatalf("first session next: %v", err)
	}
	if first.Mode != planner.ModeQuestion {
		t.Fatalf("first mode = %q", first.Mode)
	}

	if _, err := session.Next("Start with TUI"); err == nil {
		t.Fatal("expected invalid follow-up planner response to fail")
	}
}

func TestSessionNextPropagatesRunnerError(t *testing.T) {
	wantErr := errors.New("runner failed")
	session := planner.Session{
		ProjectRoot: t.TempDir(),
		Runner: &fakeRunner{
			err: wantErr,
		},
	}

	_, err := session.Next("Need a draft")
	if !errors.Is(err, wantErr) {
		t.Fatalf("runner error = %v, want %v", err, wantErr)
	}
}
