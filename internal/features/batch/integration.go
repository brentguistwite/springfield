package batch

import "fmt"

// BranchTarget returns the merge target branch for a completed slice
// given the batch's integration mode.
//
// batch      → feature/<batch-id>  (slices merge into one feature branch)
// standalone → feature/<batch-id>-<slice-id>  (each slice keeps its own branch)
// main       → main  (merge directly; use with care)
func BranchTarget(b Batch, sliceID string) (string, error) {
	switch b.IntegrationMode {
	case IntegrationBatch:
		return "feature/" + b.ID, nil
	case IntegrationStandalone:
		if sliceID == "" {
			return "", fmt.Errorf("slice id required for standalone integration mode")
		}
		return "feature/" + b.ID + "-" + sliceID, nil
	case IntegrationMain:
		return "main", nil
	default:
		return "", fmt.Errorf("unknown integration mode %q", b.IntegrationMode)
	}
}

// SliceBranchName returns the working branch name for a slice during execution.
func SliceBranchName(b Batch, sliceID string) string {
	return "springfield/" + b.ID + "/" + sliceID
}
