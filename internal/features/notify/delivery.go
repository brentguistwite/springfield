package notify

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// runner executes a constructed command. Production uses defaultRunner; tests
// inject a capturing runner so the built osascript/shell invocation can be
// asserted without spawning a real process (mirroring wakelock's injected
// exec.Command). It returns the delivery error, which the notifier logs and
// swallows — a delivery failure must never surface to the batch.
type runner func(*exec.Cmd) error

func defaultRunner(c *exec.Cmd) error { return c.Run() }

// New builds the operator's Notifier from local [notify] config. It resolves,
// in order: disabled → Nop (zero side effects, the opt-in default); a non-empty
// command → the command hook (webhook/ntfy/Slack); else on darwin → the
// built-in macOS Notification Center delivery via osascript; else → Nop (an
// enabled-but-command-less config has nothing to deliver off macOS). logw
// receives delivery-failure warnings; delivery errors are never returned.
func New(enabled bool, command, goos string, logw io.Writer) Notifier {
	return newNotifier(enabled, command, goos, defaultRunner, logw)
}

func newNotifier(enabled bool, command, goos string, run runner, logw io.Writer) Notifier {
	if !enabled {
		return Nop{}
	}
	if command != "" {
		return cmdNotifier{command: command, run: run, logw: logw}
	}
	if goos == "darwin" {
		return osaNotifier{run: run, logw: logw}
	}
	return Nop{}
}

// osaNotifier delivers to the macOS Notification Center via osascript. It adds
// no third-party dependency — osascript ships with macOS.
type osaNotifier struct {
	run  runner
	logw io.Writer
}

func (o osaNotifier) Notify(e Event) {
	script := fmt.Sprintf("display notification %s with title %s",
		osaQuote(e.message()), osaQuote("Springfield"))
	if err := o.run(exec.Command("osascript", "-e", script)); err != nil {
		_, _ = fmt.Fprintf(o.logw, "warning: notify osascript delivery failed: %v\n", err)
	}
}

// cmdNotifier runs a user-supplied command once per event via `sh -c`, with the
// event details exported as SPRINGFIELD_NOTIFY_* environment variables so the
// command can shape a webhook/ntfy/Slack payload from them.
type cmdNotifier struct {
	command string
	run     runner
	logw    io.Writer
}

func (c cmdNotifier) Notify(e Event) {
	cmd := exec.Command("sh", "-c", c.command)
	// Drop any SPRINGFIELD_NOTIFY_* the parent already exports before appending
	// this event's vars. Appending alone leaves duplicate keys in the child's
	// environment, and getenv resolves the FIRST occurrence — so a batch fired
	// from within another notify hook would let the parent's stale value shadow
	// this event's. Stripping makes the exported set deterministic.
	cmd.Env = append(envWithoutNotifyVars(os.Environ()), eventEnv(e)...)
	if err := c.run(cmd); err != nil {
		_, _ = fmt.Fprintf(c.logw, "warning: notify command failed: %v\n", err)
	}
}

// notifyEnvPrefix is the shared prefix of every event var the command hook
// exports; envWithoutNotifyVars strips inherited entries carrying it.
const notifyEnvPrefix = "SPRINGFIELD_NOTIFY_"

// envWithoutNotifyVars returns env minus any SPRINGFIELD_NOTIFY_* entries, so
// the caller can append a single authoritative copy of each event var.
func envWithoutNotifyVars(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, notifyEnvPrefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// eventEnv renders an Event as SPRINGFIELD_NOTIFY_* environment entries for the
// command hook. Every field is always present (empty when absent) so a command
// can rely on the variable set without guarding for missing keys.
func eventEnv(e Event) []string {
	return []string{
		"SPRINGFIELD_NOTIFY_KIND=" + e.Kind.slug(),
		"SPRINGFIELD_NOTIFY_BATCH_ID=" + e.BatchID,
		"SPRINGFIELD_NOTIFY_PLAN_ID=" + e.PlanID,
		"SPRINGFIELD_NOTIFY_MESSAGE=" + e.message(),
		fmt.Sprintf("SPRINGFIELD_NOTIFY_SPEND_USD=%.2f", e.SpendUSD),
		"SPRINGFIELD_NOTIFY_DETAIL=" + e.Detail,
	}
}

// message renders the human-readable notification text for an Event, used both
// as the osascript body and the SPRINGFIELD_NOTIFY_MESSAGE env value.
func (e Event) message() string {
	switch e.Kind {
	case NeedsHuman:
		return fmt.Sprintf("Batch %s needs human review", e.BatchID)
	case Complete:
		return fmt.Sprintf("Batch %s completed", e.BatchID)
	case Failed:
		if e.Detail != "" {
			return fmt.Sprintf("Batch %s failed: %s", e.BatchID, e.Detail)
		}
		return fmt.Sprintf("Batch %s failed", e.BatchID)
	case CostCapped:
		return fmt.Sprintf("Batch %s paused at cost cap ($%.2f)", e.BatchID, e.SpendUSD)
	case Stalled:
		if e.Detail != "" {
			return fmt.Sprintf("Plan %s may be wedged: no activity for %s", e.PlanID, e.Detail)
		}
		return fmt.Sprintf("Plan %s may be wedged", e.PlanID)
	default:
		return fmt.Sprintf("Batch %s reached a terminal state", e.BatchID)
	}
}

// slug is the stable machine-readable Kind name exported to the command hook.
func (k Kind) slug() string {
	switch k {
	case NeedsHuman:
		return "needs_human"
	case Complete:
		return "complete"
	case Failed:
		return "failed"
	case CostCapped:
		return "cost_capped"
	case Stalled:
		return "stalled"
	default:
		return "unknown"
	}
}

// osaQuote wraps s in an AppleScript double-quoted string literal, escaping
// backslashes and double quotes so a batch id or failure detail containing them
// can't break out of the osascript expression.
func osaQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// Compile-time proof the delivery implementations satisfy the required seam, so
// an omitted method fails the build rather than a shipped run.
var (
	_ Notifier = osaNotifier{}
	_ Notifier = cmdNotifier{}
)
