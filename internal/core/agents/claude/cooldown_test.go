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
