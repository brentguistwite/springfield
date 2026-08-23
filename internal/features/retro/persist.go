package retro

// Persist extracts a [Report] from a finished batch's archive directory,
// classifies it, and writes it back beside the archive as retro.json. It is the
// completion-path composition of [Extract], [Classify], and [WriteReport] behind
// one call, so the runner's wiring stays a single line and the ordering + priors
// policy lives here rather than leaking into cmd.
//
// priorTotalsUSD is passed nil on purpose: v1 does not gather a cross-batch
// spend baseline at finalize time, so the cost-overrun rule (which needs >= 2
// priors) stays quiet. Every other classifier runs over the freshly extracted
// report.
//
// Tolerance mirrors [Extract]: a degraded-but-valid report (missing archive.json,
// unreadable evidence) still classifies and persists. The returned error is
// reserved for a genuine failure the caller should surface — an empty batchDir
// ([Extract]), or a retro.json that could not be written ([WriteReport]) — never
// for anything merely found on disk. The (possibly degraded) report is returned
// alongside a WriteReport error so a caller can still inspect what it tried to
// persist.
func Persist(batchDir string) (*Report, error) {
	r, err := Extract(batchDir)
	if err != nil {
		return nil, err
	}
	r.Findings = Classify(r, nil)
	if err := WriteReport(batchDir, r); err != nil {
		return r, err
	}
	return r, nil
}
