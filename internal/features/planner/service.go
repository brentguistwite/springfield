package planner

import (
	"errors"
	"strings"
)

type Service struct {
	resolveProjectRoot func() (string, error)
	newConversation    func(projectRoot string) Conversation
	writeDraft         func(projectRoot, request string, response Response) error
	state              *state
}

type state struct {
	projectRoot  string
	conversation Conversation
	request      string
	answers      []string
	draft        Response
	hasDraft     bool
}

func NewService(
	resolveProjectRoot func() (string, error),
	newConversation func(projectRoot string) Conversation,
	writeDraft func(projectRoot, request string, response Response) error,
) *Service {
	return &Service{
		resolveProjectRoot: resolveProjectRoot,
		newConversation:    newConversation,
		writeDraft:         writeDraft,
	}
}

func (s *Service) Plan(input string) (PlanResult, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return PlanResult{}, errors.New("enter a work request first")
	}

	if s.state == nil || s.state.hasDraft {
		projectRoot, err := s.resolveProjectRoot()
		if err != nil {
			return PlanResult{}, err
		}
		s.state = &state{
			projectRoot:  projectRoot,
			conversation: s.newConversation(projectRoot),
			request:      trimmed,
		}
	} else {
		s.state.answers = append(s.state.answers, trimmed)
	}

	resp, err := s.state.conversation.Next(trimmed)
	if err != nil {
		return PlanResult{}, err
	}
	return s.update(resp), nil
}

func (s *Service) Regenerate() (PlanResult, error) {
	if s.state == nil || strings.TrimSpace(s.state.request) == "" {
		return PlanResult{}, errors.New("no planned work to regenerate")
	}

	request := s.state.request
	answers := append([]string(nil), s.state.answers...)
	projectRoot := s.state.projectRoot
	state := &state{
		projectRoot:  projectRoot,
		conversation: s.newConversation(projectRoot),
		request:      request,
		answers:      answers,
	}

	resp, err := state.conversation.Next(request)
	if err != nil {
		return PlanResult{}, err
	}
	for _, answer := range answers {
		if resp.Mode != ModeQuestion {
			return PlanResult{}, errors.New("planner regenerate replay did not return expected follow-up question")
		}
		resp, err = state.conversation.Next(answer)
		if err != nil {
			return PlanResult{}, err
		}
	}

	s.state = state
	return s.update(resp), nil
}

func (s *Service) Approve() error {
	if s.state == nil || !s.state.hasDraft {
		return errors.New("no planned work draft ready to approve")
	}
	if s.writeDraft == nil {
		return errors.New("planner draft writer is required")
	}

	return s.writeDraft(s.state.projectRoot, s.state.request, s.state.draft)
}

func (s *Service) Reset() {
	s.state = nil
}

func (s *Service) update(resp Response) PlanResult {
	if s.state == nil {
		return summarizePlan(resp)
	}
	if resp.Mode == ModeDraft {
		s.state.draft = resp
		s.state.hasDraft = true
	} else {
		s.state.draft = Response{}
		s.state.hasDraft = false
	}
	return summarizePlan(resp)
}

func summarizePlan(resp Response) PlanResult {
	if resp.Mode == ModeQuestion {
		return PlanResult{Question: resp.Question}
	}

	workstreams := make([]WorkstreamSummary, 0, len(resp.Workstreams))
	for _, workstream := range resp.Workstreams {
		workstreams = append(workstreams, WorkstreamSummary{
			Name:    workstream.Name,
			Title:   workstream.Title,
			Summary: workstream.Summary,
		})
	}

	return PlanResult{
		Draft: &Draft{
			WorkID:      resp.WorkID,
			Title:       resp.Title,
			Summary:     resp.Summary,
			Split:       resp.Split,
			Workstreams: workstreams,
		},
	}
}
