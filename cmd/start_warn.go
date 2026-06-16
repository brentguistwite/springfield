package cmd

import (
	"fmt"
	"io"
	"os"
	"slices"

	"springfield/internal/core/agents"
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
	// Master switch: while `claude -p` is not separately metered (Anthropic
	// reverted the 2026-05-14 change), there is no billing surprise to warn
	// about. agents.ClaudeHeadlessMetered also governs the codex-vs-claude
	// init default, so both revert together by flipping that one flag.
	if !agents.ClaudeHeadlessMetered {
		return false
	}
	if !slices.Contains(agentPriority, "claude") {
		return false
	}
	if v := os.Getenv(suppressClaudeBillingWarningEnv); v != "" && v != "0" {
		return false
	}

	low, high, batches := cost.EstimatePerPlanUSD(root, 5)
	estimate := "(no prior batches with cost data — first run will establish a baseline)"
	if batches > 0 {
		// EstimatePerPlanUSD returns a per-plan range. The active batch's
		// plan count is not known at warning time (the single-plan-unit
		// path runs before a batch exists), so we surface the per-plan
		// figure honestly and let the operator scale it by their own plan
		// count rather than fabricate a total.
		estimate = fmt.Sprintf("Estimated cost per plan: ~$%.2f–$%.2f (mean of last %d batches; multiply by your plan count for a total)", low, high, batches)
	}

	fmt.Fprintln(w, "[!] claude is in agent_priority. As of 2026-05-14, `claude -p` headless")
	fmt.Fprintln(w, "    invocations are metered separately from your Claude Max/Pro subscription:")
	fmt.Fprintln(w, "    they draw from a Console-account credit pool, then either bill at API")
	fmt.Fprintln(w, "    rates (if API billing is configured) or rate-limit mid-batch (if not).")
	fmt.Fprintf(w, "    %s\n", estimate)
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "    To use subscription-friendly agents only: remove \"claude\" from")
	fmt.Fprintln(w, "    agent_priority in springfield.toml.")
	fmt.Fprintf(w, "    To silence this warning: %s=1.\n", suppressClaudeBillingWarningEnv)
	return true
}
