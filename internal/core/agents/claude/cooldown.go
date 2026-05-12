package claude

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	coreexec "springfield/internal/core/exec"
)

// maxCooldown caps any parsed reset time at 24h beyond "now". Protects
// against weekly-limit reads or CLI bugs that report wildly distant
// reset timestamps — we'd rather re-probe and learn fresh than disable
// claude for days.
const maxCooldown = 24 * time.Hour

var (
	rePipeEpoch  = regexp.MustCompile(`usage limit reached\|(\d{9,11})`)
	reHumanTZ    = regexp.MustCompile(`(?i)reset(?:s)?\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\s*\(([^)]+)\)`)
	reHumanShort = regexp.MustCompile(`(?i)reset(?:s)?\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b`)
)

// parseCooldown inspects events, exit code, and err for a claude rate-limit
// reset timestamp. Returns the zero time when no parseable reset is found
// (caller applies a default cooldown if it still wants to skip the agent).
// Returns at most now+maxCooldown.
func parseCooldown(events []coreexec.Event, exitCode int, err error, now time.Time) time.Time {
	var b strings.Builder
	for _, e := range events {
		b.WriteString(e.Data)
		b.WriteByte('\n')
	}
	if err != nil {
		b.WriteString(err.Error())
	}
	haystack := b.String()

	if reset, ok := matchPipeEpoch(haystack); ok {
		return capReset(reset, now)
	}
	if reset, ok := matchHumanTZ(haystack, now); ok {
		return capReset(reset, now)
	}
	if reset, ok := matchHumanShort(haystack, now); ok {
		return capReset(reset, now)
	}
	return time.Time{}
}

func matchPipeEpoch(s string) (time.Time, bool) {
	m := rePipeEpoch.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	sec, convErr := strconv.ParseInt(m[1], 10, 64)
	if convErr != nil {
		return time.Time{}, false
	}
	return time.Unix(sec, 0), true
}

func matchHumanTZ(s string, now time.Time) (time.Time, bool) {
	m := reHumanTZ.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	loc, locErr := time.LoadLocation(m[4])
	if locErr != nil {
		loc = time.Local
	}
	return buildWallClock(m[1], m[2], m[3], loc, now), true
}

func matchHumanShort(s string, now time.Time) (time.Time, bool) {
	m := reHumanShort.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	return buildWallClock(m[1], m[2], m[3], time.Local, now), true
}

// buildWallClock composes the next occurrence of the given wall-clock
// time in loc, relative to now. If the computed instant is in the past
// (or equal to now), rolls forward by 24h.
func buildWallClock(hourStr, minStr, ampm string, loc *time.Location, now time.Time) time.Time {
	hour, _ := strconv.Atoi(hourStr)
	min := 0
	if minStr != "" {
		min, _ = strconv.Atoi(minStr)
	}
	switch strings.ToLower(ampm) {
	case "pm":
		if hour < 12 {
			hour += 12
		}
	case "am":
		if hour == 12 {
			hour = 0
		}
	}
	nowInLoc := now.In(loc)
	candidate := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), hour, min, 0, 0, loc)
	if !candidate.After(now) {
		candidate = candidate.Add(24 * time.Hour)
	}
	return candidate
}

func capReset(reset, now time.Time) time.Time {
	max := now.Add(maxCooldown)
	if reset.After(max) {
		return max
	}
	return reset
}
