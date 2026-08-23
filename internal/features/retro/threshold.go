package retro

// Threshold constants gate which aggregated [Pattern] rows are worth acting on:
// a pattern must recur often enough, and across enough distinct batches, before
// Springfield files a ticket for it. They are exported named constants — not
// inline magic numbers at the comparison site — so callers and tests can name
// the exact boundary the filer keys on.
//
// A pattern qualifies only when BOTH hold. Occurrences alone is not enough: a
// single flaky batch can trip the same key three times in one run, and that is
// noise, not a recurring failure mode. Requiring MinBatches distinct batches is
// what makes the signal "keeps happening across runs" rather than "happened a
// lot once".
const (
	// MinOccurrences is the fewest total findings carrying a pattern key, across
	// every in-window report, for the pattern to clear the bar (>= 3).
	MinOccurrences = 3
	// MinBatches is the fewest distinct batches a pattern must span to clear the
	// bar (>= 2), so a single noisy batch cannot promote a pattern on its own.
	MinBatches = 2
)

// AboveThreshold reports whether a [Pattern] clears both the occurrence and the
// batch-spread bar and is therefore worth filing. The recency window is not
// applied here — the caller narrows the corpus by passing a since to [Aggregate]
// before the patterns ever reach this check.
func AboveThreshold(p Pattern) bool {
	return p.Occurrences >= MinOccurrences && len(p.Batches) >= MinBatches
}

// Filter returns the subset of patterns that clear [AboveThreshold], preserving
// the input order (Aggregate already sorts by occurrences descending, ties by
// key). It never returns nil for a non-nil input: an all-filtered corpus yields
// an empty, non-nil slice so callers can range without a nil check.
func Filter(patterns []Pattern) []Pattern {
	kept := make([]Pattern, 0, len(patterns))
	for _, p := range patterns {
		if AboveThreshold(p) {
			kept = append(kept, p)
		}
	}
	return kept
}
