// Package retro turns a finished Springfield batch into a structured
// retrospective. It is the read side of the self-learning loop: where
// batch.FinalizeBatch writes the archive entry and relocates per-plan evidence
// under .springfield/archive/<batchID>/plans/<key>/, retro reads that layout
// back and folds it into a single [Report].
//
// The package is deliberately a leaf: it depends only on the batch and cost
// packages for their on-the-wire record shapes (ArchiveEntry, cost.Capture) and
// nothing depends on it, so Extract can evolve the retro model without rippling
// into the run path. It is the sibling of cost.EstimatePerPlanUSD — both are
// after-the-fact archive readers — but where that one estimates spend, this one
// reconstructs what happened well enough to classify it.
//
// The exported surface is small: [Extract] over a batch archive directory
// yields a *[Report]; the report carries a batch header, one [PlanRetro] per
// plan, and (once classifiers land) a slice of [Finding]. [WriteReport] persists
// that report atomically back beside the archive as retro.json. Everything else
// is an internal detail of how the finalized archive layout is walked.
package retro
