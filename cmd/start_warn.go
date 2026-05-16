package cmd

import (
	"fmt"
	"io"
	"os"
	"slices"

	"springfield/internal/features/cost"
)

const suppressClaudeBillingWarningEnv = "SPRINGFIELD_SUPPRESS_CLAUDE_BILLING_WARNING"

// emitClaudeBillingWarning prints the vendor-economics warning to w when the
// project's agent_priority list contains "claude" and the suppression env
// var is not set. Returns whether the warning was emitted (callers may use
// the return value for tests).
//
// The estimate uses cost.EstimatePerPlanUSD against the project's archive
// dir. When no archive entries with TotalUSD>0 exist (fresh project or
// pre-PR legacy archives only), the message prints "(no prior batches)"
// rather than a dollar range so the operator is not misled by a fabricated
// number.
func emitClaudeBillingWarning(w io.Writer, root string, agentPriority []string) bool {
	if !slices.Contains(agentPriority, "claude") {
		return false
	}
	if v := os.Getenv(suppressClaudeBillingWarningEnv); v != "" && v != "0" {
		return false
	}

	low, high, batches := cost.EstimatePerPlanUSD(root, 5)
	estimate := "(no prior batches with cost data — first run will establish a baseline)"
	if batches > 0 {
		estimate = fmt.Sprintf("Estimated cost for this batch: ~$%.2f–$%.2f (mean of last %d batches × plan count)", low, high, batches)
	}

	fmt.Fprintln(w, "[!] claude is in agent_priority. As of 2026-05-14, `claude -p` headless")
	fmt.Fprintln(w, "    invocations bill against your ANTHROPIC_API_KEY at API rates, not your")
	fmt.Fprintln(w, "    Claude Max/Pro subscription.")
	fmt.Fprintf(w, "    %s\n", estimate)
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "    To use subscription-friendly agents only: remove \"claude\" from")
	fmt.Fprintln(w, "    agent_priority in springfield.toml.")
	fmt.Fprintf(w, "    To silence this warning: %s=1.\n", suppressClaudeBillingWarningEnv)
	return true
}
