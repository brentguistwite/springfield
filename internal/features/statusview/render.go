package statusview

import (
	"fmt"
	"strings"
	"time"
)

// Render formats a View as a plain-text watch frame: a header line drawn from
// the projection's Summary, then one row per plan carrying its board status,
// in-flight activity (phase/detail/round), the agent in use, and elapsed time.
// It reads ONLY the passed View, so a watcher and `--json` present the same
// projection — never a second source of truth.
//
// now is the reference clock for per-plan elapsed time; the caller passes
// time.Now() per tick (tests pass a fixed instant for determinism). Only a
// running plan with a recorded StartedAt shows elapsed — a stale duration on a
// finished or unstarted plan would be a lie, so the row omits it.
//
// The output is a self-contained frame with no terminal escapes; the CLI redraw
// loop owns clearing the screen between frames.
func Render(v View, now time.Time) string {
	var b strings.Builder

	summary := v.Summary
	if summary == "" {
		summary = "No status available."
	}
	fmt.Fprintln(&b, summary)

	for _, p := range v.Plans {
		fmt.Fprintf(&b, "  %s  %s", p.ID, p.Status)
		if act := formatActivity(p.Activity); act != "" {
			fmt.Fprintf(&b, "  %s", act)
		}
		if p.Agent != "" {
			fmt.Fprintf(&b, "  [%s]", p.Agent)
		}
		if p.Status == StatusRunning && p.StartedAt != nil {
			fmt.Fprintf(&b, "  %s", formatElapsed(now.Sub(*p.StartedAt)))
		}
		fmt.Fprintln(&b)
	}
	return b.String()
}

// formatActivity renders an in-flight ActivityView as a compact one-liner: the
// coarse phase, the optional detail (current story / human phrase), then the
// optional fine round counter — e.g. "implementing US-001 (round 3)". A nil
// activity (any non-running plan) yields the empty string so the row shows no
// invented phase.
func formatActivity(av *ActivityView) string {
	if av == nil {
		return ""
	}
	out := av.Phase
	if av.Detail != "" {
		out += " " + av.Detail
	}
	if av.Round > 0 {
		out += fmt.Sprintf(" (round %d)", av.Round)
	}
	return out
}

// formatElapsed renders a running plan's elapsed wall time compactly. A negative
// span (clock skew between now and StartedAt) clamps to 0s rather than printing
// a nonsensical negative duration.
func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	s := int((d % time.Minute) / time.Second)
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
