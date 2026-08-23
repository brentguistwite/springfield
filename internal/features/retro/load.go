package retro

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Load reads the persisted retro.json from a batch archive directory — the read
// counterpart to [WriteReport], for a consumer (status rendering) that wants the
// derived report without re-running [Extract] and [Classify] over the archive.
//
// It draws a deliberate line between "no report" and "bad report": an absent
// retro.json returns (nil, nil) — a batch that predates or skipped retro
// extraction simply has none, which is not an error — while a present-but-corrupt
// file returns a decode error. A status surface that must degrade to silence on
// either can treat (nil, _) OR a non-nil error as "render nothing"; a consumer
// that cares to distinguish a missing report from a broken one still can.
func Load(batchDir string) (*Report, error) {
	if strings.TrimSpace(batchDir) == "" {
		return nil, fmt.Errorf("retro: batchDir must not be empty")
	}
	data, err := os.ReadFile(filepath.Join(batchDir, retroFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("retro: read report: %w", err)
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("retro: decode report: %w", err)
	}
	return &r, nil
}

// Summary is a compact rollup of a [Report]'s findings for a status surface: the
// total number of findings plus the single most-prominent pattern key and the
// number of plans that tripped it. It carries just enough for a one-line
// digest — the full findings live in the report itself.
type Summary struct {
	TotalFindings int
	TopPatternKey string
	// TopCount is how many plans tripped the top pattern (len of its PlanIDs). A
	// batch-level finding with no plans (e.g. cost-overrun) contributes 0, so a
	// consumer can drop the count from its render rather than print "x0".
	TopCount int
}

// Summarize reduces a report's findings to a compact [Summary]. The top pattern
// is the finding tripped by the most plans — the widest-spread signal — with ties
// broken by the classifiers' stable declaration order (findings arrive in
// planRules order, batch-level rules last), so the same report always summarizes
// identically. A nil report or one with no findings yields the zero Summary,
// which a caller renders as nothing extra.
func Summarize(r *Report) Summary {
	if r == nil || len(r.Findings) == 0 {
		return Summary{}
	}
	s := Summary{TotalFindings: len(r.Findings)}
	best := -1
	for _, f := range r.Findings {
		// Strictly-greater keeps the first finding at a given count, preserving the
		// classifiers' declaration order on ties.
		if n := len(f.PlanIDs); n > best {
			best = n
			s.TopPatternKey = f.PatternKey
			s.TopCount = n
		}
	}
	return s
}
