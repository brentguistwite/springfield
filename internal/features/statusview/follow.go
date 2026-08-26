package statusview

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// TraceEvent is one decoded line of the live agent-trace JSONL stream written
// by the runner (cmd/start.go openAgentTrace). It is the ONLY live per-event
// source: per-slice evidence events.jsonl is written post-hoc after a
// subprocess exits and cannot be followed live. Concurrent plans share one
// serialized append file, so every line carries its Plan id — the field a
// follower filters on.
type TraceEvent struct {
	Type string `json:"type"`
	Time string `json:"time"`
	Data string `json:"data"`
	Plan string `json:"plan"`
}

// FilterTrace decodes agent-trace JSONL from r and writes only planID's event
// data lines to w, one per line. It is the pure core of per-plan follow: given
// a trace containing several plans' interleaved events, exactly one plan's
// output renders. Malformed lines and events with empty data are skipped
// (best-effort diagnostic stream); it errors only on a write failure.
func FilterTrace(w io.Writer, r io.Reader, planID string) error {
	sc := bufio.NewScanner(r)
	// Trace lines wrap agent stdout events (stream-json tool_results); size the
	// buffer like the exec scanner so a large line is not silently truncated.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var ev TraceEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if ev.Plan != planID || ev.Data == "" {
			continue
		}
		if _, err := fmt.Fprintln(w, ev.Data); err != nil {
			return err
		}
	}
	return sc.Err()
}

// LatestTracePath returns the newest agent-trace file for batchID under
// .springfield/logs (files are named "<batchID>-<ts>.agent-trace.jsonl"). ok is
// false when the batch has not written a trace yet — the follower treats that
// as "nothing to stream yet", not an error, because the trace is created lazily
// once the first agent event fires. It only reads the logs directory listing.
func LatestTracePath(root, batchID string) (path string, ok bool, err error) {
	glob := filepath.Join(root, ".springfield", "logs", batchID+"-*.agent-trace.jsonl")
	matches, err := filepath.Glob(glob)
	if err != nil {
		return "", false, err
	}
	if len(matches) == 0 {
		return "", false, nil
	}
	// Lexical order matches chronological order: the timestamp segment is a
	// fixed-width UTC "20060102T150405Z" stamp, so the largest string is newest.
	sort.Strings(matches)
	return matches[len(matches)-1], true, nil
}

// TailTrace reads new complete lines of the trace at tracePath starting at
// offset, writes planID's event data to w, and returns the advanced offset. It
// is read-only: the file is opened O_RDONLY and only whole lines (up to the
// last newline) are consumed, so a half-written trailing line is re-read on the
// next tick rather than dropped or double-emitted. A not-yet-created trace
// returns the offset unchanged (nil error) — the runner writes it lazily.
func TailTrace(w io.Writer, tracePath string, offset int64, planID string) (int64, error) {
	f, err := os.Open(tracePath)
	if err != nil {
		if os.IsNotExist(err) {
			return offset, nil
		}
		return offset, err
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return offset, err
	}
	lastNL := bytes.LastIndexByte(data, '\n')
	if lastNL < 0 {
		return offset, nil // no complete line yet
	}
	complete := data[:lastNL+1]
	if err := FilterTrace(w, bytes.NewReader(complete), planID); err != nil {
		return offset, err
	}
	return offset + int64(len(complete)), nil
}

// TraceFollower is the stateful driver over TailTrace for a multi-tick follow
// loop. It carries the current trace path alongside its consumed offset because
// the offset is only meaningful relative to the file it was measured against: a
// batch restart/resume rolls the trace over to a NEW timestamped file (see
// cmd/start.go openAgentTrace), so an offset advanced into the old file would
// Seek past the head of the new, shorter one and silently drop its opening
// events. On a path change Tail first drains the old file's unread tail (events
// appended after the last tick but before the roll) and only then resets the
// offset to 0, preserving the read-only, no-re-emit, no-drop guarantees of
// TailTrace across the roll as well as within each file.
type TraceFollower struct {
	path   string
	offset int64
}

// Tail streams planID's new trace lines at tracePath to w, resuming from the
// prior offset when tracePath is unchanged and restarting from 0 when it rolls
// over to a new file. It is read-only and delegates the whole-line, lazy-file
// semantics to TailTrace; only the cross-file drain-and-reset lives here.
func (f *TraceFollower) Tail(w io.Writer, tracePath, planID string) error {
	if tracePath != f.path {
		// Rollover: the batch restarted/resumed onto a new timestamped trace
		// file. Events can have been appended to the OLD file between our last
		// tick and the switch (a follow loop stays alive across an interrupt —
		// the batch view-state remains "active" while a plan is merely stalled —
		// so it can still be tailing when a resume rolls the trace). Drain that
		// unread remainder FIRST so a rollover never silently drops the old
		// stream's final events, then reset to the new file's head. The old
		// file is read once from f.offset (no re-emit) and left untouched
		// (read-only); its advanced offset is discarded since we switch away.
		if f.path != "" {
			if _, err := TailTrace(w, f.path, f.offset, planID); err != nil {
				return err
			}
		}
		f.path = tracePath
		f.offset = 0
	}
	off, err := TailTrace(w, tracePath, f.offset, planID)
	if err != nil {
		return err
	}
	f.offset = off
	return nil
}
