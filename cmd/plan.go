package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// NewPlanCommand compiles a Springfield batch from a caller-provided slice payload.
// TODO(phase-3): full rewrite pending PRD ingest.
func NewPlanCommand() *cobra.Command {
	var dir string
	var slicesArg string
	var replace bool
	var appendMode bool

	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Compile a Springfield plan into a runnable batch.",
		Long: "Compile a Springfield plan from a caller-provided slice payload.\n\n" +
			"Use --slices <path> to read a JSON payload from a file, or --slices - to read from stdin.\n" +
			"The springfield:plan skill emits this payload. Run \"springfield start\" to execute.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if slicesArg == "" {
				return fmt.Errorf("--slices is required (path to JSON payload, or \"-\" for stdin)")
			}
			_ = dir
			_ = replace
			_ = appendMode
			// TODO(phase-3): batch ingest pending PRD rewrite
			return errors.New("cmd/plan: pending Phase 3 rewrite")
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".", "project root or nested path inside the Springfield project")
	cmd.Flags().StringVar(&slicesArg, "slices", "", "path to slice payload JSON, or \"-\" to read from stdin")
	cmd.Flags().BoolVar(&replace, "replace", false, "archive the current active batch and replace it with this one")
	cmd.Flags().BoolVar(&appendMode, "append", false, "add new slices to the end of the current active batch")

	return cmd
}
