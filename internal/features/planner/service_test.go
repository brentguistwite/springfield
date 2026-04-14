package planner_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/features/planner"
	"springfield/internal/features/workflow"
)

type fakeConversation struct {
	inputs    []string
	responses []planner.Response
	err       error
}

func (f *fakeConversation) Next(input string) (planner.Response, error) {
	f.inputs = append(f.inputs, input)
	if f.err != nil {
		return planner.Response{}, f.err
	}
	if len(f.responses) == 0 {
		return planner.Response{}, fmt.Errorf("unexpected planner call for %q", input)
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func TestServicePlanReturnsQuestionAndDraft(t *testing.T) {
	root := t.TempDir()
	conversation := &fakeConversation{
		responses: []planner.Response{
			{Mode: planner.ModeQuestion, Question: "Which workflow surface should ship first?"},
			{
				Mode:    planner.ModeDraft,
				WorkID:  "wave-c1",
				Title:   "Wave C1 planning loop",
				Summary: "Connect the planning flow to the real planner session.",
				Split:   planner.SplitSingle,
				Workstreams: []planner.Workstream{
					{Name: "01", Title: "Implement Wave C1", Summary: "Keep it in one stream."},
				},
			},
		},
	}

	service := planner.NewService(
		func() (string, error) { return root, nil },
		func(projectRoot string) planner.Conversation { return conversation },
		func(projectRoot, request string, response planner.Response) error { return nil },
	)

	first, err := service.Plan("Connect the planner")
	if err != nil {
		t.Fatalf("first plan: %v", err)
	}
	if got, want := first.Question, "Which workflow surface should ship first?"; got != want {
		t.Fatalf("question = %q, want %q", got, want)
	}

	second, err := service.Plan("Start with New Work")
	if err != nil {
		t.Fatalf("second plan: %v", err)
	}
	if second.Draft == nil || second.Draft.WorkID != "wave-c1" {
		t.Fatalf("expected reviewable draft, got %#v", second)
	}
	if got, want := len(conversation.inputs), 2; got != want {
		t.Fatalf("planner calls = %d, want %d", got, want)
	}
}

func TestServiceApproveWritesWorkflowDraft(t *testing.T) {
	root := t.TempDir()
	service := planner.NewService(
		func() (string, error) { return root, nil },
		func(projectRoot string) planner.Conversation {
			return &fakeConversation{
				responses: []planner.Response{
					{
						Mode:    planner.ModeDraft,
						WorkID:  "wave-c1",
						Title:   "Wave C1 planning loop",
						Summary: "Connect the planner to the runtime flow.",
						Split:   planner.SplitSingle,
						Workstreams: []planner.Workstream{
							{Name: "01", Title: "Implement Wave C1", Summary: "Keep it in one stream."},
						},
					},
				},
			}
		},
		func(projectRoot, request string, response planner.Response) error {
			return plannerWriteDraft(projectRoot, request, response)
		},
	)

	if _, err := service.Plan("Connect the planner to the runtime flow"); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := service.Approve(); err != nil {
		t.Fatalf("approve: %v", err)
	}

	requestPath := filepath.Join(root, ".springfield", "work", "wave-c1", "request.md")
	body, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("read request.md: %v", err)
	}
	if got, want := string(body), "Connect the planner to the runtime flow"; got != want {
		t.Fatalf("request body = %q, want %q", got, want)
	}
}

func TestServiceRegenerateReplaysAnswers(t *testing.T) {
	root := t.TempDir()
	conversations := []*fakeConversation{
		{
			responses: []planner.Response{
				{Mode: planner.ModeQuestion, Question: "Which workflow surface should ship first?"},
				{
					Mode:    planner.ModeDraft,
					WorkID:  "wave-c1",
					Title:   "Original",
					Summary: "Original draft.",
					Split:   planner.SplitSingle,
					Workstreams: []planner.Workstream{
						{Name: "01", Title: "Original"},
					},
				},
			},
		},
		{
			responses: []planner.Response{
				{Mode: planner.ModeQuestion, Question: "Which workflow surface should ship first?"},
				{
					Mode:    planner.ModeDraft,
					WorkID:  "wave-c1b",
					Title:   "Regenerated",
					Summary: "Regenerated draft.",
					Split:   planner.SplitSingle,
					Workstreams: []planner.Workstream{
						{Name: "01", Title: "Regenerated"},
					},
				},
			},
		},
	}

	service := planner.NewService(
		func() (string, error) { return root, nil },
		func(projectRoot string) planner.Conversation {
			conversation := conversations[0]
			conversations = conversations[1:]
			return conversation
		},
		func(projectRoot, request string, response planner.Response) error { return nil },
	)

	if _, err := service.Plan("Connect the planner"); err != nil {
		t.Fatalf("first plan: %v", err)
	}
	if _, err := service.Plan("Start with New Work"); err != nil {
		t.Fatalf("second plan: %v", err)
	}

	result, err := service.Regenerate()
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if result.Draft == nil || result.Draft.Title != "Regenerated" {
		t.Fatalf("expected regenerated draft, got %#v", result)
	}
	if got, want := conversations, []*fakeConversation{}; len(got) != len(want) {
		t.Fatalf("expected regenerate to consume a fresh conversation")
	}
}

func plannerWriteDraft(projectRoot, request string, response planner.Response) error {
	return workflow.WriteDraft(projectRoot, workflow.Draft{
		RequestBody: request,
		Response:    response,
	})
}
