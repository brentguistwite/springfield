package execution

import (
	"encoding/json"
	"os"
	"path/filepath"

	"springfield/internal/features/cost"
)

// WriteCost persists a per-iteration cost capture to <iterDir>/cost.json
// using the same atomic temp+rename discipline as the rest of the evidence
// writes. The directory is created if it does not exist so callers can use
// WriteCost without also calling WriteEvidence first (the warning-only run
// or test fixtures benefit from that).
func WriteCost(iterDir string, c cost.Capture) error {
	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeEvidenceFile(filepath.Join(iterDir, "cost.json"), data)
}
