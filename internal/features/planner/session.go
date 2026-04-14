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
	request     string
	pending     string
	turns       []turn
}

// Next asks the planner for the next question or a draft response.
func (s *Session) Next(userInput string) (Response, error) {
	if s.Runner == nil {
		return Response{}, fmt.Errorf("planner runner is required")
	}

	input := strings.TrimSpace(userInput)
	switch {
	case s.request == "":
		s.request = input
	case s.pending != "":
		s.turns = append(s.turns, turn{
			Question: s.pending,
			Answer:   input,
		})
		s.pending = ""
	case input != "":
		s.turns = append(s.turns, turn{
			Answer: input,
		})
	}

	output, err := playbooks.Build(playbooks.Input{
		Purpose:     playbooks.PurposePlan,
		ProjectRoot: s.ProjectRoot,
		TaskBody:    planningTask(s.request, s.turns),
	})
	if err != nil {
		return Response{}, fmt.Errorf("build planning prompt: %w", err)
	}

	raw, err := s.Runner.Run(output.Prompt)
	if err != nil {
		return Response{}, err
	}

	var resp Response
	if err := json.Unmarshal([]byte(stripFences(raw)), &resp); err != nil {
		return Response{}, fmt.Errorf("decode planner response: %w", err)
	}
	if err := Validate(resp); err != nil {
		return Response{}, fmt.Errorf("validate planner response: %w", err)
	}
	if resp.Mode == ModeQuestion {
		s.pending = strings.TrimSpace(resp.Question)
	} else {
		s.pending = ""
	}

	return resp, nil
}

func stripFences(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return trimmed
	}
	lines = lines[1:]
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

type turn struct {
	Question string
	Answer   string
}

func planningTask(request string, turns []turn) string {
	var builder strings.Builder

	builder.WriteString("Plan the user's request for Springfield.\n\n")
	builder.WriteString("Initial request:\n")
	builder.WriteString(request)
	builder.WriteString("\n\n")

	if len(turns) == 0 {
		builder.WriteString("Follow-up answers: none yet.\n\n")
	} else {
		builder.WriteString("Planning conversation so far:\n")
		for _, turn := range turns {
			if turn.Question != "" {
				builder.WriteString("Planner question: ")
				builder.WriteString(turn.Question)
				builder.WriteString("\n")
			}
			if turn.Answer != "" {
				builder.WriteString("User answer: ")
				builder.WriteString(turn.Answer)
				builder.WriteString("\n")
			}
			builder.WriteString("\n")
		}
	}

	builder.WriteString("Return JSON only. No markdown fences. No prose before or after the JSON.\n\n")
	builder.WriteString("JSON contract:\n")
	builder.WriteString("- mode: \"question\" or \"draft\"\n")
	builder.WriteString("- question: required when mode is \"question\"\n")
	builder.WriteString("- work_id: required when mode is \"draft\"\n")
	builder.WriteString("- title: required when mode is \"draft\"\n")
	builder.WriteString("- summary: required when mode is \"draft\"\n")
	builder.WriteString("- split: \"single\" or \"multi\" when mode is \"draft\"\n")
	builder.WriteString("- workstreams: at least one item when mode is \"draft\"\n")
	builder.WriteString("- each workstream needs name, title, and optional summary\n")

	return strings.TrimSpace(builder.String())
}
