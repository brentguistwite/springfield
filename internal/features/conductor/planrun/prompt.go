package planrun

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"springfield/internal/features/conductor/planrun/prompts"
	"springfield/internal/features/prd"
)

// promptData is the template data shape shared by both header and footer
// templates. Footer templates receive the same struct so operator overrides
// can reference plan/story fields from either template.
type promptData struct {
	PlanID, PlanTitle, PlanDescription                string
	StoryID, StoryTitle, StoryDescription, StoryNotes string
	AcceptanceCriteria                                []string
	RemainingStories                                  []remainingStory
	ContextMD                                         string
	ProjectGuidance                                   string
}

type remainingStory struct {
	ID, Title string
	Passes    bool
}

// BuildPromptForPlan assembles the agent prompt: header + plan metadata +
// context.md (verbatim, if present) + current-story details + footer.
//
// Operator override: if <projectRoot>/springfield/prompts/{header,footer}.tmpl
// exists and parses, that file is used in place of the embedded template.
// Missing override file = silent fallback to embed (expected, common).
// Parse error on present override = loud fail (no silent fallback).
//
// projectRoot is the project's config root (NOT os.Getwd which can drift if
// the binary is invoked from a subdir). Caller passes it in.
func BuildPromptForPlan(plan prd.PRD, contextMD string, projectGuidance string,
	currentStory prd.UserStory, projectRoot string) (string, error) {

	// Build remaining stories list (all except the current one that aren't passed).
	var remaining []remainingStory
	for _, s := range plan.UserStories {
		if s.ID == currentStory.ID {
			continue
		}
		remaining = append(remaining, remainingStory{
			ID:     s.ID,
			Title:  s.Title,
			Passes: s.Passes,
		})
	}

	data := promptData{
		PlanID:             plan.ID,
		PlanTitle:          plan.Title,
		PlanDescription:    plan.Description,
		StoryID:            currentStory.ID,
		StoryTitle:         currentStory.Title,
		StoryDescription:   currentStory.Description,
		StoryNotes:         currentStory.Notes,
		AcceptanceCriteria: currentStory.AcceptanceCriteria,
		RemainingStories:   remaining,
		ContextMD:          contextMD,
		ProjectGuidance:    projectGuidance,
	}

	headerStr, err := loadTemplate(projectRoot, "header.tmpl", prompts.HeaderTmpl)
	if err != nil {
		return "", err
	}
	footerStr, err := loadTemplate(projectRoot, "footer.tmpl", prompts.FooterTmpl)
	if err != nil {
		return "", err
	}

	header, err := renderTemplate("header", headerStr, data)
	if err != nil {
		return "", fmt.Errorf("render header template: %w", err)
	}
	footer, err := renderTemplate("footer", footerStr, data)
	if err != nil {
		return "", fmt.Errorf("render footer template: %w", err)
	}

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	if contextMD != "" {
		b.WriteString("# Plan context\n")
		b.WriteString(contextMD)
		if !strings.HasSuffix(contextMD, "\n") {
			b.WriteString("\n")
		}
	}
	if projectGuidance != "" {
		b.WriteString("# Project guidance\n")
		b.WriteString(projectGuidance)
		if !strings.HasSuffix(projectGuidance, "\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString(footer)

	return b.String(), nil
}

// BuildReviewFixPrompt builds the prompt for a review fix-iteration: the same
// header/footer envelope as a story prompt, but the body instructs the
// implementer to address the reviewer's findings (all stories already pass, so
// there is no current story) and re-emit the completion marker.
func BuildReviewFixPrompt(plan prd.PRD, contextMD, projectGuidance, findings, projectRoot string) (string, error) {
	data := promptData{
		PlanID:          plan.ID,
		PlanTitle:       plan.Title,
		PlanDescription: plan.Description,
		ContextMD:       contextMD,
		ProjectGuidance: projectGuidance,
	}
	headerStr, err := loadTemplate(projectRoot, "header.tmpl", prompts.HeaderTmpl)
	if err != nil {
		return "", err
	}
	footerStr, err := loadTemplate(projectRoot, "footer.tmpl", prompts.FooterTmpl)
	if err != nil {
		return "", err
	}
	header, err := renderTemplate("header", headerStr, data)
	if err != nil {
		return "", fmt.Errorf("render header template: %w", err)
	}
	footer, err := renderTemplate("footer", footerStr, data)
	if err != nil {
		return "", fmt.Errorf("render footer template: %w", err)
	}
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n\nA reviewer examined your completed work and requires changes before it can be merged. Address every point below, then re-emit <promise>COMPLETE</promise> when the work is ready for re-review.\n\nREVIEW FINDINGS:\n")
	b.WriteString(findings)
	b.WriteString("\n\n")
	b.WriteString(footer)
	return b.String(), nil
}

// loadTemplate returns the template string for name. Tries operator override
// at <projectRoot>/springfield/prompts/<name> first. Missing = silent fallback
// to embedded. Present but parse-failing = loud error.
func loadTemplate(projectRoot, name, embedded string) (string, error) {
	overridePath := filepath.Join(projectRoot, "springfield", "prompts", name)
	data, err := os.ReadFile(overridePath)
	if err != nil {
		if os.IsNotExist(err) {
			return embedded, nil
		}
		return "", fmt.Errorf("read override template %s: %w", overridePath, err)
	}
	// Verify it parses — loud fail if broken.
	if _, parseErr := template.New(name).Parse(string(data)); parseErr != nil {
		return "", fmt.Errorf("parse override template %s: %w", overridePath, parseErr)
	}
	return string(data), nil
}

func renderTemplate(name, tmplStr string, data promptData) (string, error) {
	tmpl, err := template.New(name).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse %s template: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute %s template: %w", name, err)
	}
	return buf.String(), nil
}
