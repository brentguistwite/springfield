package claude

import (
	"strconv"
	"testing"
	"time"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
)

func ev(stream coreexec.EventType, line string) coreexec.Event {
	return coreexec.Event{Type: stream, Data: line, Time: time.Time{}}
}

func TestParseCooldown_PipeEpoch(t *testing.T) {
	events := []coreexec.Event{
		ev(coreexec.EventStdout, `{"type":"result","subtype":"error","is_error":true,"message":{"content":[{"type":"text","text":"Claude AI usage limit reached|1759770000"}]}}`),
	}
	now := time.Unix(1759700000, 0)

	got := parseCooldown(events, 1, nil, now)

	want := time.Unix(1759770000, 0)
	if !got.Equal(want) {
		t.Fatalf("parseCooldown pipe-epoch: got %v want %v", got, want)
	}
}

func TestParseCooldown_HumanWithTimezone(t *testing.T) {
	events := []coreexec.Event{
		ev(coreexec.EventStderr, "Claude usage limit reached. Your limit will reset at 1pm (America/New_York)."),
	}
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, loc)

	got := parseCooldown(events, 1, nil, now)

	want := time.Date(2026, 5, 11, 13, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("parseCooldown human/tz: got %v want %v", got, want)
	}
}

func TestParseCooldown_HumanShortForm(t *testing.T) {
	events := []coreexec.Event{
		ev(coreexec.EventStdout, "5-hour limit reached - resets 3pm"),
	}
	loc := time.Local
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, loc)

	got := parseCooldown(events, 1, nil, now)

	want := time.Date(2026, 5, 11, 15, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("parseCooldown short form: got %v want %v", got, want)
	}
}

func TestParseCooldown_ResetAlreadyPassed_RollsToNextDay(t *testing.T) {
	events := []coreexec.Event{
		ev(coreexec.EventStdout, "Your limit will reset at 9am (America/New_York)."),
	}
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 5, 11, 14, 0, 0, 0, loc)

	got := parseCooldown(events, 1, nil, now)

	want := time.Date(2026, 5, 12, 9, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("parseCooldown rollover: got %v want %v", got, want)
	}
}

func TestParseCooldown_Raw429NoTimestamp_ReturnsZero(t *testing.T) {
	events := []coreexec.Event{
		ev(coreexec.EventStderr, `{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your account's rate limit. Please try again later."}}`),
	}
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)

	got := parseCooldown(events, 1, nil, now)

	if !got.IsZero() {
		t.Fatalf("parseCooldown raw 429: expected zero time, got %v", got)
	}
}

func TestParseCooldown_FalsePositive_ConnectionReset(t *testing.T) {
	// Cross-event-boundary haystack join must not let "reset" in one event
	// pair up with a time fragment in another.
	events := []coreexec.Event{
		ev(coreexec.EventStderr, "connection reset by peer"),
		ev(coreexec.EventStderr, "retrying at 3pm"),
	}
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)

	got := parseCooldown(events, 1, nil, now)

	if !got.IsZero() {
		t.Fatalf("false positive on cross-boundary reset/3pm: got %v want zero", got)
	}
}

func TestParseCooldown_FalsePositive_CounterReset(t *testing.T) {
	// Stderr noise that mentions "reset N" should not be parsed as a
	// rate-limit reset — there's no rate-limit context.
	events := []coreexec.Event{
		ev(coreexec.EventStderr, "internal counter reset 5 times during retry"),
	}
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)

	got := parseCooldown(events, 1, nil, now)

	if !got.IsZero() {
		t.Fatalf("false positive on 'counter reset 5': got %v want zero", got)
	}
}

func TestParseCooldown_DSTSpringForward_RollsCorrectly(t *testing.T) {
	// US DST spring-forward 2026-03-08 02:00 EST → 03:00 EDT.
	// A naive Add(24h) rollover crosses the DST gap and lands an hour
	// off the wall-clock time the operator actually saw in the message.
	loc, _ := time.LoadLocation("America/New_York")
	// Saturday noon EST, before DST jump.
	now := time.Date(2026, 3, 7, 12, 0, 0, 0, loc)
	events := []coreexec.Event{
		ev(coreexec.EventStderr, "Your limit will reset at 11am (America/New_York)."),
	}

	got := parseCooldown(events, 1, nil, now)

	// Sunday 11am EDT — wall-clock 11am next day, not 12pm.
	want := time.Date(2026, 3, 8, 11, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("DST rollover: got %v want %v", got, want)
	}
}

func TestParseCooldown_FalsePositive_UsageInUnrelatedLog(t *testing.T) {
	// "usage" + reset + digit + ampm on the same line, but unrelated to
	// claude rate limits. Currently passes the keyword gate; tightening
	// the regex (must include a Claude-style rate-limit verb pair) blocks it.
	events := []coreexec.Event{
		ev(coreexec.EventStderr, "API usage cap: connection reset at 3pm yesterday"),
	}
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)

	got := parseCooldown(events, 1, nil, now)

	if !got.IsZero() {
		t.Fatalf("false positive on 'usage cap...reset at 3pm': got %v want zero", got)
	}
}

func TestParseCooldown_NoAmPm_ReturnsZero(t *testing.T) {
	// "reset at 5 minutes" must not match — there's no am/pm marker, so
	// the "5" is not a wall-clock hour.
	events := []coreexec.Event{
		ev(coreexec.EventStderr, "limit will reset at 5 minutes from now"),
	}
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)

	got := parseCooldown(events, 1, nil, now)

	if !got.IsZero() {
		t.Fatalf("false positive on 'reset at 5 minutes': got %v want zero", got)
	}
}

func TestParseCooldown_UnknownTimezone_ReturnsZero(t *testing.T) {
	// Bad/unknown timezone names must fail closed (zero time) so the
	// runner applies the default cooldown rather than silently guessing
	// local wall-clock.
	events := []coreexec.Event{
		ev(coreexec.EventStderr, "Your limit will reset at 1pm (Atlantis/Lost)."),
	}
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)

	got := parseCooldown(events, 1, nil, now)

	if !got.IsZero() {
		t.Fatalf("unknown TZ should return zero, got %v", got)
	}
}

func TestParseCooldown_PastPipeEpoch_ReturnsZero(t *testing.T) {
	// If the parsed reset epoch is already in the past, return zero so
	// the runner applies the default cooldown instead of installing an
	// already-expired entry.
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	pastEpoch := now.Add(-1 * time.Hour).Unix()
	events := []coreexec.Event{
		ev(coreexec.EventStdout, "Claude AI usage limit reached|"+strconv.FormatInt(pastEpoch, 10)),
	}

	got := parseCooldown(events, 1, nil, now)

	if !got.IsZero() {
		t.Fatalf("past epoch should return zero, got %v", got)
	}
}

// Real-world format observed in anthropics/claude-code issue 8620:
// "Claude usage limit reached. Your limit will reset at Oct 7, 1am."
// Date prefix between "at" and time. Within 24h cap → parsed precisely.
func TestParseCooldown_DatedWallClock_WithinCap(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 10, 6, 22, 0, 0, 0, loc)
	events := []coreexec.Event{
		ev(coreexec.EventStderr, "Claude usage limit reached. Your limit will reset at Oct 7, 1am."),
	}

	got := parseCooldown(events, 1, nil, now)

	want := time.Date(2026, 10, 7, 1, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("dated wallclock: got %v want %v", got, want)
	}
}

// Same issue-8620 message but the reset is 6 days out — should clamp to
// the 24h cap rather than fall through to a default cooldown.
func TestParseCooldown_DatedWallClock_BeyondCap_ClampsTo24h(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, loc)
	events := []coreexec.Event{
		ev(coreexec.EventStderr, "Claude usage limit reached. Your limit will reset at Oct 7, 1am."),
	}

	got := parseCooldown(events, 1, nil, now)

	want := now.Add(24 * time.Hour)
	if !got.Equal(want) {
		t.Fatalf("dated wallclock cap: got %v want %v", got, want)
	}
}

// Full month name + no comma variant.
func TestParseCooldown_DatedWallClock_FullMonthName(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 10, 6, 22, 0, 0, 0, loc)
	events := []coreexec.Event{
		ev(coreexec.EventStderr, "Your limit will reset at October 7 1am."),
	}

	got := parseCooldown(events, 1, nil, now)

	want := time.Date(2026, 10, 7, 1, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("full month name: got %v want %v", got, want)
	}
}

func TestParseCooldown_NoRateLimitMessage_ReturnsZero(t *testing.T) {
	events := []coreexec.Event{
		ev(coreexec.EventStderr, "panic: nil pointer dereference"),
	}
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)

	got := parseCooldown(events, 1, nil, now)

	if !got.IsZero() {
		t.Fatalf("parseCooldown unrelated stderr: expected zero, got %v", got)
	}
}

func TestAdapterImplementsCooldowner(t *testing.T) {
	a := New(nil)
	if _, ok := a.(agents.Cooldowner); !ok {
		t.Fatal("claude adapter does not implement agents.Cooldowner")
	}
}

// Verifies the adapter wrapper correctly forwards events/now to parseCooldown
// (catches pass-through mistakes — wrong argument order, dropped `now`, etc.).
func TestAdapterCooldown_ForwardsNowToParser(t *testing.T) {
	a := New(nil).(agents.Cooldowner)
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: "Claude AI usage limit reached|1759770000"},
	}
	now := time.Unix(1759700000, 0)

	got := a.Cooldown(events, 1, nil, now)

	want := time.Unix(1759770000, 0)
	if !got.Equal(want) {
		t.Fatalf("adapter.Cooldown pipe-epoch: got %v want %v", got, want)
	}
}

// Verifies the adapter passes now through to the wall-clock branch so a
// pinned clock produces deterministic output.
func TestAdapterCooldown_WallClockUsesProvidedNow(t *testing.T) {
	a := New(nil).(agents.Cooldowner)
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, loc)
	events := []coreexec.Event{
		{Type: coreexec.EventStderr, Data: "Your limit will reset at 1pm (America/New_York)."},
	}

	got := a.Cooldown(events, 1, nil, now)

	want := time.Date(2026, 5, 11, 13, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("adapter.Cooldown wall-clock: got %v want %v", got, want)
	}
}

func TestParseCooldown_CapsAt24Hours(t *testing.T) {
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	weekOut := now.Add(7 * 24 * time.Hour).Unix()
	events := []coreexec.Event{
		ev(coreexec.EventStdout, "Claude AI usage limit reached|"+strconv.FormatInt(weekOut, 10)),
	}

	got := parseCooldown(events, 1, nil, now)

	max := now.Add(24 * time.Hour)
	if got.After(max) {
		t.Fatalf("parseCooldown cap: got %v exceeds 24h cap %v", got, max)
	}
	if !got.Equal(max) {
		t.Fatalf("parseCooldown cap: got %v want exactly %v", got, max)
	}
}
