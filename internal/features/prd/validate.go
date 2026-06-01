package prd

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	contextMDWarnLimit  = 32 * 1024  // 32 KB ≈ 8K tokens
	contextMDErrorLimit = 256 * 1024 // 256 KB hard limit
)

var (
	planIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	storyIDPattern = regexp.MustCompile(`^US-\d{3,}$`)

	// Verifiability heuristics for acceptance criteria. A criterion is treated
	// as "verifiable" (no warning) if it carries any concrete, checkable
	// signal: a command/outcome keyword, an HTTP verb, or a file-path/extension
	// shape. The intent is a low-false-positive nudge, not a gate — when in
	// doubt the criterion is left unwarned. See validate.go's isVerifiable.
	httpVerbPattern = regexp.MustCompile(`\b(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b`)
	// `logs` (not `logs?`) on purpose: bare `log` collides with prose like
	// "user can log in", which is not a verifiable signal.
	verifiableKeyword = regexp.MustCompile(`(?i)\b(?:test|tests|passes|passing|fails?|exits?|builds?|compiles?|lints?|returns?|responds?|status|asserts?|equals?|matches?|contains?|exists?|present|endpoint|command|coverage|output|logs)\b`)
	// Extension arm requires 2+ letters after the dot so Latin abbreviations
	// ("e.g.", "i.e.", "N.B.") don't read as file extensions and suppress the
	// warning. Real extensions (.go, .md, .ts) are always 2+.
	filePathOrExtRegex = regexp.MustCompile(`[\w-]+/[\w./-]+|\.[a-zA-Z]{2,6}\b`)
)

// isVerifiable reports whether an acceptance criterion carries a concrete,
// checkable signal. It is intentionally permissive: any of a command/outcome
// keyword, an HTTP verb, a file-path or extension shape, a digit (status/exit
// codes, counts), or a code-ish token (backtick, parens, =) marks the criterion
// verifiable. Only criteria with none of these are warned as vague.
func isVerifiable(criterion string) bool {
	c := strings.TrimSpace(criterion)
	if c == "" {
		return false // empty handled separately as a hard error
	}
	switch {
	case verifiableKeyword.MatchString(c):
		return true
	case httpVerbPattern.MatchString(c):
		return true
	case filePathOrExtRegex.MatchString(c):
		return true
	case strings.ContainsAny(c, "0123456789"):
		return true // status codes, exit codes, counts, thresholds
	case strings.ContainsAny(c, "`(){}="):
		return true // code-ish tokens
	default:
		return false
	}
}

// Validate checks a BatchPRDEnvelope for structural correctness and returns
// all errors and warnings found. It never bails early — callers receive the
// full aggregated result so every problem can be reported at once.
func Validate(env BatchPRDEnvelope) ValidationResult {
	var res ValidationResult

	// Envelope-level checks.
	if strings.TrimSpace(env.Title) == "" {
		res.Errors = append(res.Errors, fmt.Errorf("envelope: title is required"))
	}
	if strings.TrimSpace(env.Source) == "" {
		res.Errors = append(res.Errors, fmt.Errorf("envelope: source is required"))
	}
	if len(env.Plans) == 0 {
		res.Errors = append(res.Errors, fmt.Errorf("envelope: at least one plan is required"))
	}
	if len(env.Phases) == 0 {
		res.Errors = append(res.Errors, fmt.Errorf("envelope: at least one phase is required"))
	}

	// Build plan ID set for phase and dep resolution.
	planIDs := make(map[string]bool, len(env.Plans))
	seenPlanIDs := make(map[string]bool, len(env.Plans))
	for _, p := range env.Plans {
		if seenPlanIDs[p.ID] {
			res.Errors = append(res.Errors, fmt.Errorf("envelope: duplicate plan id %q", p.ID))
		}
		seenPlanIDs[p.ID] = true
		planIDs[p.ID] = true
	}

	// Phase plan ID resolution.
	referencedByPhase := make(map[string]bool, len(env.Plans))
	for i, phase := range env.Phases {
		for _, pid := range phase.Plans {
			if !planIDs[pid] {
				res.Errors = append(res.Errors, fmt.Errorf("phase[%d]: plan id %q not found in plans", i, pid))
			}
			referencedByPhase[pid] = true
		}
	}

	// Inverse check: every plan must be referenced by at least one phase.
	for _, p := range env.Plans {
		if !referencedByPhase[p.ID] {
			res.Errors = append(res.Errors, fmt.Errorf("plan %q is not referenced by any phase", p.ID))
		}
	}

	// Plan-level and story-level checks.
	for _, plan := range env.Plans {
		validatePlan(plan, &res)
	}

	return res
}

func validatePlan(plan BatchPRDPlan, res *ValidationResult) {
	prefix := fmt.Sprintf("plan %q", plan.ID)

	if !planIDPattern.MatchString(plan.ID) {
		res.Errors = append(res.Errors, fmt.Errorf("%s: id must match ^[a-z0-9][a-z0-9-]*$", prefix))
	}
	if strings.TrimSpace(plan.Title) == "" {
		res.Errors = append(res.Errors, fmt.Errorf("%s: title is required", prefix))
	}

	// context_md size checks.
	ctxLen := len(plan.ContextMD)
	if ctxLen > contextMDErrorLimit {
		res.Errors = append(res.Errors, fmt.Errorf("%s: context_md exceeds 256 KB hard limit (%d bytes)", prefix, ctxLen))
	} else if ctxLen > contextMDWarnLimit {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s: context_md is large (%d bytes, recommend ≤32 KB)", prefix, ctxLen))
	}

	if len(plan.UserStories) == 0 {
		res.Errors = append(res.Errors, fmt.Errorf("%s: at least one user story is required", prefix))
	}

	// Build story ID set within this plan for dep validation.
	storyIDs := make(map[string]bool, len(plan.UserStories))
	seenStoryIDs := make(map[string]bool, len(plan.UserStories))
	for _, s := range plan.UserStories {
		if seenStoryIDs[s.ID] {
			res.Errors = append(res.Errors, fmt.Errorf("%s: duplicate story id %q", prefix, s.ID))
		}
		seenStoryIDs[s.ID] = true
		storyIDs[s.ID] = true
	}

	for _, story := range plan.UserStories {
		validateStory(story, plan.ID, storyIDs, res)
	}
}

func validateStory(story UserStory, planID string, planStoryIDs map[string]bool, res *ValidationResult) {
	prefix := fmt.Sprintf("plan %q story %q", planID, story.ID)

	if !storyIDPattern.MatchString(story.ID) {
		res.Errors = append(res.Errors, fmt.Errorf("%s: id does not match ^US-\\d{3,}$ (got %q); non-conforming ids cannot be marked by the story-pass scanner", prefix, story.ID))
	}
	if strings.TrimSpace(story.Title) == "" {
		res.Errors = append(res.Errors, fmt.Errorf("%s: title is required", prefix))
	}
	if story.Priority < 1 {
		res.Errors = append(res.Errors, fmt.Errorf("%s: priority must be >= 1 (got %d)", prefix, story.Priority))
	}
	if len(story.AcceptanceCriteria) == 0 {
		res.Errors = append(res.Errors, fmt.Errorf("%s: at least one acceptance criterion is required", prefix))
	}

	for i, criterion := range story.AcceptanceCriteria {
		if strings.TrimSpace(criterion) == "" {
			// A blank/whitespace-only criterion is no Definition of Done at all —
			// hard error, not a warning. (An empty *slice* is caught above.)
			res.Errors = append(res.Errors, fmt.Errorf("%s: acceptance criterion %d is empty", prefix, i+1))
			continue
		}
		if !isVerifiable(criterion) {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"%s: acceptance criterion %d looks hard to verify (%q) — consider a concrete check like a test command, file path, or HTTP response",
				prefix, i+1, criterion))
		}
	}

	for _, dep := range story.Deps {
		if !planStoryIDs[dep] {
			res.Errors = append(res.Errors, fmt.Errorf("%s: dep %q not found in same plan's stories", prefix, dep))
		}
	}
}
