package planreview

import (
	"fmt"
	"strings"
)

// defaultReviewMethodology is the built-in reviewer instruction used when the
// operator supplies no custom [review].prompt. Operators override it with their
// own methodology (e.g. "Run the adversarial-review skill on this diff.").
const defaultReviewMethodology = `Act as a rigorous, independent code reviewer. Scrutinize the diff for correctness bugs, missing edge cases, broken or absent tests, and any failure to meet the stated acceptance criteria. Be adversarial but fair.`

// reviewPromptShell is the Springfield-owned protocol envelope. It ALWAYS wraps
// the methodology so the verdict protocol, acceptance criteria, and diff are
// present even when the operator supplies a custom prompt — Springfield owns the
// protocol; the operator owns only the methodology.
// Verbs: %[1]s = methodology, %[2]s = criteria, %[3]s = diff.
const reviewPromptShell = `%[1]s

You are reviewing a completed unit of work before it is merged into the feature branch. Emit EXACTLY ONE verdict marker on its own line, then write your findings as prose:

  <review-verdict>pass</review-verdict>   — satisfies the acceptance criteria; safe to merge.
  <review-verdict>revise</review-verdict> — has problems an agent can fix; describe each so the implementer can address it.
  <review-verdict>halt</review-verdict>   — needs a human; not safely fixable by an agent (ambiguous requirements, risky design call, etc.).

ACCEPTANCE CRITERIA:
%[2]s

DIFF UNDER REVIEW:
%[3]s`

// BuildReviewPrompt composes the reviewer prompt. customPrompt (from
// [review].prompt) supplies the methodology; empty/whitespace → the built-in
// default. The verdict protocol, criteria, and diff are always injected.
func BuildReviewPrompt(diff string, criteria []string, customPrompt string) string {
	methodology := strings.TrimSpace(customPrompt)
	if methodology == "" {
		methodology = defaultReviewMethodology
	}
	return fmt.Sprintf(reviewPromptShell, methodology, formatCriteria(criteria), diff)
}

func formatCriteria(criteria []string) string {
	if len(criteria) == 0 {
		return "(none specified)"
	}
	var b strings.Builder
	for _, c := range criteria {
		fmt.Fprintf(&b, "- %s\n", c)
	}
	return strings.TrimRight(b.String(), "\n")
}
