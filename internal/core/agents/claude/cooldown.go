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
	rePipeEpoch = regexp.MustCompile(`usage limit reached\|(\d{9,11})`)
	// am/pm required: without it, "reset at 5 minutes" would match with
	// hour=5, producing a bogus 5am cooldown. Real claude reset messages
	// always carry an am/pm suffix.
	reHumanTZ    = regexp.MustCompile(`(?i)reset(?:s)?\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)\s*\(([^)]+)\)`)
	reHumanShort = regexp.MustCompile(`(?i)reset(?:s)?\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)\b`)
	// reRateLimitPhrase gates wall-clock matching: a line must contain an
	// actual rate-limit phrase, not merely the words "limit" or "usage"
	// in isolation, so unrelated stderr like
	// "API usage cap: connection reset at 3pm yesterday" cannot match.
	reRateLimitPhrase = regexp.MustCompile(`(?i)(usage limit|rate[- ]?limit|hour limit|limit (?:will\s+)?reset|limit reached)`)
)

// parseCooldown inspects events, exit code, and err for a claude rate-limit
// reset timestamp. Returns the zero time when no parseable reset is found
// (caller applies a default cooldown if it still wants to skip the agent).
// Returns at most now+maxCooldown.
//
// Wall-clock regexes (reHumanTZ, reHumanShort) are gated by the presence of
// the word "limit" on the same line so unrelated stderr noise like
// "connection reset by peer" or "counter reset 5 times" cannot trigger a
// false positive. Pipe-epoch already requires "usage limit reached" in its
// own pattern so it scans across the whole haystack.
func parseCooldown(events []coreexec.Event, exitCode int, err error, now time.Time) time.Time {
	lines := collectLines(events, err)
	joined := strings.Join(lines, "\n")

	if reset, ok := matchPipeEpoch(joined); ok {
		return capReset(reset, now)
	}
	for _, line := range lines {
		if !hasLimitContext(line) {
			continue
		}
		// If the line carries a parenthesized timezone, it MUST resolve;
		// silently falling through to the no-TZ branch would let an
		// unknown zone (e.g. "(Atlantis/Lost)") install a local-time
		// cooldown that's hours off the operator's actual reset.
		if reHumanTZ.MatchString(line) {
			if reset, ok := matchHumanTZ(line, now); ok {
				return capReset(reset, now)
			}
			continue
		}
		if reset, ok := matchHumanShort(line, now); ok {
			return capReset(reset, now)
		}
	}
	return time.Time{}
}

func collectLines(events []coreexec.Event, err error) []string {
	var lines []string
	for _, e := range events {
		for _, l := range strings.Split(e.Data, "\n") {
			lines = append(lines, l)
		}
	}
	if err != nil {
		for _, l := range strings.Split(err.Error(), "\n") {
			lines = append(lines, l)
		}
	}
	return lines
}

func hasLimitContext(line string) bool {
	return reRateLimitPhrase.MatchString(line)
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
		// Fail closed: silently guessing time.Local could land hours off
		// the operator's actual reset. Let the runner apply the default
		// cooldown instead.
		return time.Time{}, false
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
		// AddDate (not Add(24h)) preserves wall-clock time across DST
		// transitions. Add(24h) crosses the DST boundary as a fixed
		// duration and lands an hour off the operator-visible reset time.
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

func capReset(reset, now time.Time) time.Time {
	if !reset.After(now) {
		// Already-expired reset (stale epoch, clock skew). Return zero so
		// the caller applies its default cooldown rather than installing
		// an entry that will be skipped immediately.
		return time.Time{}
	}
	max := now.Add(maxCooldown)
	if reset.After(max) {
		return max
	}
	return reset
}
