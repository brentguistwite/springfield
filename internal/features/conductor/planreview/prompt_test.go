package planreview_test

import (
	"strings"
	"testing"

	"springfield/internal/features/conductor/planreview"
)

func TestBuildReviewPromptDefaultMethodologyAndProtocol(t *testing.T) {
	got := planreview.BuildReviewPrompt("DIFF-BODY", []string{"AC one", "AC two"}, "")
	for _, want := range []string{
		"<review-verdict>pass</review-verdict>",
		"<review-verdict>revise</review-verdict>",
		"<review-verdict>halt</review-verdict>",
		"DIFF-BODY",
		"- AC one",
		"- AC two",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("default prompt missing %q\n---\n%s", want, got)
		}
	}
}

func TestBuildReviewPromptCustomMethodologyReplacesDefaultButKeepsProtocol(t *testing.T) {
	custom := "Run the adversarial-review skill on this diff."
	got := planreview.BuildReviewPrompt("DIFF-BODY", []string{"AC"}, custom)
	if !strings.Contains(got, custom) {
		t.Fatalf("custom methodology missing from prompt:\n%s", got)
	}
	if !strings.Contains(got, "<review-verdict>revise</review-verdict>") || !strings.Contains(got, "DIFF-BODY") {
		t.Fatalf("custom prompt dropped protocol/diff:\n%s", got)
	}
}

func TestBuildReviewPromptCustomWhitespaceFallsBackToDefault(t *testing.T) {
	got := planreview.BuildReviewPrompt("D", nil, "   ")
	if !strings.Contains(got, "independent code reviewer") {
		t.Fatalf("whitespace custom should fall back to default methodology:\n%s", got)
	}
}

func TestBuildReviewPromptEmptyCriteria(t *testing.T) {
	got := planreview.BuildReviewPrompt("D", nil, "")
	if !strings.Contains(got, "(none specified)") {
		t.Fatalf("empty criteria should render placeholder:\n%s", got)
	}
}
