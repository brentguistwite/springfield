package planner

import (
	"encoding/json"
	"fmt"
	"strings"

	"springfield/internal/features/playbooks"
)

// Runner is the narrow planner model boundary.
type Runner interface {
	Run(prompt string) (string, error)
}

// Session builds one planning prompt and validates one structured response.
type Session struct {
	ProjectRoot string
	Runner      Runner
}

// Next asks the planner for the next question or a draft response.
func (s Session) Next(userInput string) (Response, error) {
	if s.Runner == nil {
		return Response{}, fmt.Errorf("planner runner is required")
	}

	output, err := playbooks.Build(playbooks.Input{
		Kind:        playbooks.KindConductor,
		ProjectRoot: s.ProjectRoot,
		TaskBody:    planningTask(strings.TrimSpace(userInput)),
	})
	if err != nil {
		return Response{}, fmt.Errorf("build planning prompt: %w", err)
	}

	raw, err := s.Runner.Run(output.Prompt)
	if err != nil {
		return Response{}, err
	}

	var resp Response
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return Response{}, fmt.Errorf("decode planner response: %w", err)
	}
	if err := Validate(resp); err != nil {
		return Response{}, fmt.Errorf("validate planner response: %w", err)
	}

	return resp, nil
}

func planningTask(userInput string) string {
	return strings.TrimSpace(`
Plan the user's request for Springfield.

User request:
` + userInput + `

Return JSON only. No markdown fences. No prose before or after the JSON.

JSON contract:
- mode: "question" or "draft"
- question: required when mode is "question"
- work_id: required when mode is "draft"
- title: required when mode is "draft"
- summary: required when mode is "draft"
- split: "single" or "multi" when mode is "draft"
- workstreams: at least one item when mode is "draft"
- each workstream needs name, title, and optional summary
`)
}
