// Package notify surfaces a batch's terminal states to the operator who
// launched an unattended run and would otherwise poll status: a plan pausing
// for human review, the batch completing, failing, or halting on a --cost-cap.
//
// The Notifier interface is the seam. The batch terminal-state handling in
// cmd/start.go holds one Notifier and fires an Event at each terminal state;
// delivery (macOS Notification Center via osascript, a configurable command
// hook) lives entirely behind implementations of Notifier's single required
// method. Nop is the built-in default used when notifications are not
// configured, matching the opt-in, off-by-default policy.
package notify

// Kind identifies which batch terminal state produced an Event.
type Kind int

const (
	// NeedsHuman: a plan paused for human review, halting the batch until the
	// operator resolves it.
	NeedsHuman Kind = iota
	// Complete: the batch finished all plans and archived cleanly.
	Complete
	// Failed: the batch halted on an unrecoverable plan failure.
	Failed
	// CostCapped: the batch paused when total spend reached the --cost-cap
	// threshold. It is resumable, not failed.
	CostCapped
)

// Event describes one batch terminal state worth surfacing to the operator.
// The optional fields carry only the context relevant to their Kind; a field
// left at its zero value is simply absent from the delivered notification.
type Event struct {
	// Kind is the terminal state that fired the Event.
	Kind Kind
	// BatchID names the batch this Event is about.
	BatchID string
	// SpendUSD is the batch spend at the moment CostCapped fired; 0 otherwise.
	SpendUSD float64
	// Detail carries the failure message for Failed; empty otherwise.
	Detail string
}

// Notifier delivers a terminal-state Event to the operator.
//
// Notify is a required interface method (AGENTS.md principle 5): the batch
// seam holds a Notifier and calls Notify directly, never discovering delivery
// through a type assertion that could silently miss in production. A Notify
// call must never fail the batch — implementations swallow their own delivery
// errors rather than propagate them.
type Notifier interface {
	Notify(Event)
}

// Nop is the built-in default Notifier: it delivers nothing. It is wired in
// when the operator has not configured notifications, so the seam is always
// invoked (never nil) while staying silent by default.
type Nop struct{}

// Notify does nothing.
func (Nop) Notify(Event) {}

// Compile-time proof that Nop satisfies the required seam. When delivery
// implementations land they pin themselves the same way, so an omitted method
// fails the build rather than a shipped run.
var _ Notifier = Nop{}
