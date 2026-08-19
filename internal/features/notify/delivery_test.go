package notify

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// capture is a runner that records the *exec.Cmd it was handed instead of
// spawning a process, so a test can assert the constructed invocation (argv +
// env) without an OS dependency. err is what it returns to the notifier.
type capture struct {
	cmds []*exec.Cmd
	err  error
}

func (c *capture) run(cmd *exec.Cmd) error {
	c.cmds = append(c.cmds, cmd)
	return c.err
}

// TestOsascriptInvocationForSampleEvent pins the exact osascript command the
// macOS built-in delivery constructs for a sample event: `osascript -e
// 'display notification "<msg>" with title "Springfield"'`. No third-party
// dependency and no real process — the invocation is asserted, not executed.
func TestOsascriptInvocationForSampleEvent(t *testing.T) {
	cap := &capture{}
	var buf bytes.Buffer
	n := newNotifier(true, "", "darwin", cap.run, &buf)

	n.Notify(Event{Kind: NeedsHuman, BatchID: "batch-7"})

	if len(cap.cmds) != 1 {
		t.Fatalf("got %d invocations, want 1", len(cap.cmds))
	}
	got := cap.cmds[0].Args
	want := []string{
		"osascript", "-e",
		`display notification "Batch batch-7 needs human review" with title "Springfield"`,
	}
	if len(got) != len(want) {
		t.Fatalf("osascript args = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("osascript arg[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if buf.Len() != 0 {
		t.Fatalf("successful delivery logged: %q", buf.String())
	}
}

// TestDisabledProducesZeroSideEffects proves the opt-in default: an absent /
// disabled config fires no runner call and writes no log, for every Kind. It
// checks both the exported New (production wiring) returns Nop and that the
// injected-runner path is never touched.
func TestDisabledProducesZeroSideEffects(t *testing.T) {
	if _, ok := New(false, "", "darwin", nil).(Nop); !ok {
		t.Fatalf("New(disabled) = %T, want notify.Nop", New(false, "", "darwin", nil))
	}

	cap := &capture{}
	var buf bytes.Buffer
	n := newNotifier(false, "cmd that would run", "darwin", cap.run, &buf)
	for _, k := range []Kind{NeedsHuman, Complete, Failed, CostCapped} {
		n.Notify(Event{Kind: k, BatchID: "b1"})
	}
	if len(cap.cmds) != 0 {
		t.Fatalf("disabled notifier ran %d commands, want 0", len(cap.cmds))
	}
	if buf.Len() != 0 {
		t.Fatalf("disabled notifier logged %q, want nothing", buf.String())
	}
}

// TestCommandRunsPerEventWithEventDetails proves the [notify] command hook runs
// once per event via `sh -c`, with the event details exported as
// SPRINGFIELD_NOTIFY_* environment variables the command can consume.
func TestCommandRunsPerEventWithEventDetails(t *testing.T) {
	cap := &capture{}
	var buf bytes.Buffer
	n := newNotifier(true, "curl -d \"$SPRINGFIELD_NOTIFY_MESSAGE\" example", "linux", cap.run, &buf)

	n.Notify(Event{Kind: CostCapped, BatchID: "b9", SpendUSD: 12.5})
	n.Notify(Event{Kind: Failed, BatchID: "b9", Detail: "boom"})

	if len(cap.cmds) != 2 {
		t.Fatalf("got %d command runs, want one per event (2)", len(cap.cmds))
	}
	first := cap.cmds[0]
	if len(first.Args) != 3 || first.Args[0] != "sh" || first.Args[1] != "-c" {
		t.Fatalf("command invocation = %#v, want sh -c <command>", first.Args)
	}
	if first.Args[2] != "curl -d \"$SPRINGFIELD_NOTIFY_MESSAGE\" example" {
		t.Fatalf("command body = %q", first.Args[2])
	}
	env := envMap(first.Env)
	if env["SPRINGFIELD_NOTIFY_KIND"] != "cost_capped" {
		t.Errorf("KIND = %q, want cost_capped", env["SPRINGFIELD_NOTIFY_KIND"])
	}
	if env["SPRINGFIELD_NOTIFY_BATCH_ID"] != "b9" {
		t.Errorf("BATCH_ID = %q, want b9", env["SPRINGFIELD_NOTIFY_BATCH_ID"])
	}
	if env["SPRINGFIELD_NOTIFY_SPEND_USD"] != "12.50" {
		t.Errorf("SPEND_USD = %q, want 12.50", env["SPRINGFIELD_NOTIFY_SPEND_USD"])
	}
	if !strings.Contains(env["SPRINGFIELD_NOTIFY_MESSAGE"], "cost cap") {
		t.Errorf("MESSAGE = %q, want it to mention the cost cap", env["SPRINGFIELD_NOTIFY_MESSAGE"])
	}
	if got := envMap(cap.cmds[1].Env)["SPRINGFIELD_NOTIFY_DETAIL"]; got != "boom" {
		t.Errorf("second event DETAIL = %q, want boom", got)
	}
}

// TestNotifyFailureLoggedAndSwallowed proves a failing/misconfigured notify
// command logs and continues: Notify never panics and never propagates the
// error (its signature has no return), so a batch outcome cannot change.
func TestNotifyFailureLoggedAndSwallowed(t *testing.T) {
	cap := &capture{err: errors.New("exit status 127")}
	var buf bytes.Buffer
	n := newNotifier(true, "definitely-not-a-real-binary", "linux", cap.run, &buf)

	n.Notify(Event{Kind: Complete, BatchID: "b1"}) // must not panic

	if buf.Len() == 0 {
		t.Fatal("notify failure was not logged")
	}
	if !strings.Contains(buf.String(), "exit status 127") {
		t.Fatalf("log %q should carry the delivery error", buf.String())
	}
}

// TestCommandEnvOverridesInheritedNotifyVars proves the command hook's event
// vars are deterministic even when the parent process already exports a
// SPRINGFIELD_NOTIFY_* value: the child sees exactly one entry per key, holding
// this event's value. Appending to os.Environ() alone would leave a duplicate
// key whose FIRST (stale parent) occurrence getenv resolves — a real delivery
// bug that envMap's last-wins folding would otherwise mask.
func TestCommandEnvOverridesInheritedNotifyVars(t *testing.T) {
	t.Setenv("SPRINGFIELD_NOTIFY_KIND", "stale-parent-value")

	cap := &capture{}
	var buf bytes.Buffer
	n := newNotifier(true, "true", "linux", cap.run, &buf)

	n.Notify(Event{Kind: Complete, BatchID: "b1"})

	if len(cap.cmds) != 1 {
		t.Fatalf("got %d command runs, want 1", len(cap.cmds))
	}
	var kinds []string
	for _, kv := range cap.cmds[0].Env {
		if strings.HasPrefix(kv, "SPRINGFIELD_NOTIFY_KIND=") {
			kinds = append(kinds, strings.TrimPrefix(kv, "SPRINGFIELD_NOTIFY_KIND="))
		}
	}
	if len(kinds) != 1 {
		t.Fatalf("SPRINGFIELD_NOTIFY_KIND appears %d times %v, want exactly 1 (no inherited duplicate)", len(kinds), kinds)
	}
	if kinds[0] != "complete" {
		t.Fatalf("SPRINGFIELD_NOTIFY_KIND = %q, want complete (event value, not inherited)", kinds[0])
	}
}

func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}
