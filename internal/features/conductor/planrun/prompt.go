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
	"springfield/internal/features/verify"
)

// verifyPromptTailBytes caps each captured stream embedded in a verify-fix
// prompt. The evidence writer keeps a far larger tail on disk (256KB) because
// disk is cheap; a prompt must stay small so the failure transcript does not
// crowd out the agent's context. The actionable failure is at the END of a
// verify transcript, so oversized streams are truncated from the front.
const verifyPromptTailBytes = 8 * 1024

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
	b.WriteString("\n\nA reviewer examined your completed work and requires changes before it can be merged. Address every point below, COMMIT your fixes (the next review round diffs HEAD against the base ref — uncommitted changes are invisible to the reviewer AND to the eventual merge), then re-emit <promise>COMPLETE</promise> when the work is ready for re-review.\n\nREVIEW FINDINGS:\n")
	b.WriteString(findings)
	b.WriteString("\n\n")
	b.WriteString(footer)
	return b.String(), nil
}

// BuildVerifyFixPrompt builds the prompt for a verify fix-iteration. The verify
// command exited non-zero (or timed out), so the same header/footer envelope as
// a story prompt wraps a body that reports the command, its exit code, the
// tail of its failure output, and the plan's acceptance criteria — then demands
// a genuine fix and re-emission of the completion marker.
//
// It takes the same (Request, Result) pair the verify package pairs everywhere
// (see verify.WriteEvidence) so the exit code and output are read from the real
// round outcome, never re-derived at the call site. The no-test-weakening
// instruction is load-bearing: the gate is only objective if the fix agent
// cannot pass it by deleting, skipping, or gutting the failing tests.
func BuildVerifyFixPrompt(plan prd.PRD, contextMD, projectGuidance, projectRoot string, req verify.Request, res verify.Result) (string, error) {
	var criteria []string
	for _, s := range plan.UserStories {
		criteria = append(criteria, s.AcceptanceCriteria...)
	}

	data := promptData{
		PlanID:             plan.ID,
		PlanTitle:          plan.Title,
		PlanDescription:    plan.Description,
		AcceptanceCriteria: criteria,
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

	// Describe the exit condition. A timeout reports ExitCode -1, which is
	// meaningless on its own, so the timeout is named explicitly instead.
	exitLine := fmt.Sprintf("Exit code: %d", res.ExitCode)
	if res.TimedOut {
		exitLine = fmt.Sprintf("The command TIMED OUT and was killed (exit code %d).", res.ExitCode)
	}

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n\nThe verify command must exit 0 before this plan can be honored complete, but it FAILED. Diagnose and fix the ROOT CAUSE in the product or test code, COMMIT your fix (the next verify round runs against your committed tree), then re-emit <promise>COMPLETE</promise>.\n\n")
	b.WriteString("Verify command: ")
	b.WriteString(req.Command)
	b.WriteString("\n")
	b.WriteString(exitLine)
	b.WriteString("\n\n")
	if len(criteria) > 0 {
		b.WriteString("This work must still satisfy every acceptance criterion:\n")
		for _, c := range criteria {
			b.WriteString("- ")
			b.WriteString(c)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("Failure output (tail):\n")
	if out := verifyFailureOutput(res); out != "" {
		b.WriteString(out)
		if !strings.HasSuffix(out, "\n") {
			b.WriteString("\n")
		}
	} else {
		b.WriteString("(no output captured)\n")
	}
	b.WriteString("\nDo NOT weaken, skip, delete, or otherwise neuter tests to make the command pass — that defeats the gate and the reviewer will catch it. Make the tests genuinely pass by fixing the underlying defect.\n\n")
	b.WriteString(footer)
	return b.String(), nil
}

// verifyFailureOutput renders the tail of the round's stderr and stdout for the
// fix prompt. Both streams are labelled and independently tail-truncated so the
// actionable end of each is preserved without either crowding out the other.
func verifyFailureOutput(res verify.Result) string {
	var parts []string
	if s := strings.TrimSpace(res.Stdout); s != "" {
		parts = append(parts, "[stdout]\n"+promptTail(res.Stdout))
	}
	if s := strings.TrimSpace(res.Stderr); s != "" {
		parts = append(parts, "[stderr]\n"+promptTail(res.Stderr))
	}
	return strings.Join(parts, "\n")
}

// promptTail keeps the last verifyPromptTailBytes of s, prefixing a notice when
// the front was dropped so the agent knows the transcript is elided.
func promptTail(s string) string {
	if len(s) <= verifyPromptTailBytes {
		return s
	}
	tail := s[len(s)-verifyPromptTailBytes:]
	return fmt.Sprintf("[springfield: output truncated, showing last %d of %d bytes]\n%s", verifyPromptTailBytes, len(s), tail)
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
