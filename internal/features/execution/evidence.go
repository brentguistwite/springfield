package execution

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	coreexec "springfield/internal/core/exec"
)

const assistantTextLimitBytes = 256 * 1024

type EvidenceSnapshot struct {
	AgentID        string
	Model          string
	ExitCode       int
	Classification string
	Prompt         string
	Events         []coreexec.Event
	StartedAt      time.Time
	EndedAt        time.Time
	Err            error
}

func WriteEvidence(dir string, snap EvidenceSnapshot) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	metaBytes, err := json.MarshalIndent(evidenceMetaFromSnapshot(snap), "", "  ")
	if err != nil {
		return err
	}
	if err := writeEvidenceFile(filepath.Join(dir, "meta.json"), metaBytes); err != nil {
		return err
	}
	eventBytes, err := marshalEventsJSONL(snap.Events)
	if err != nil {
		return err
	}
	if err := writeEvidenceFile(filepath.Join(dir, "events.jsonl"), eventBytes); err != nil {
		return err
	}
	if err := writeEvidenceFile(filepath.Join(dir, "assistant_text.txt"), assistantTextFromEvents(snap.Events)); err != nil {
		return err
	}
	if err := writeEvidenceFile(filepath.Join(dir, "prompt.txt"), []byte(snap.Prompt)); err != nil {
		return err
	}
	return nil
}

// StallRecord is one possibly-wedged classification appended to a plan's
// evidence when event-recency stall detection flags a silent agent. Recording
// each occurrence (rather than only the current PlanState signal, which a
// terminal transition overwrites) keeps recurring wedges diagnosable post-hoc.
type StallRecord struct {
	PlanID     string    `json:"plan_id"`
	Iteration  int       `json:"iteration,omitempty"`
	StaleFor   string    `json:"stale_for"`
	ObservedAt time.Time `json:"observed_at"`
}

// stallRecordFile is the append-only JSONL log of wedge occurrences, one record
// per line, living in the plan's evidence directory beside meta.json.
const stallRecordFile = "stalls.jsonl"

// AppendStallRecord appends one wedge occurrence to dir/stalls.jsonl, creating
// the directory and file as needed. It is append-only (O_APPEND) so concurrent
// or repeated wedges accumulate a full history rather than overwriting; each
// record is a single JSON line. Called from the stall watcher's escalation
// callback — advisory only, it never touches the subprocess.
func AppendStallRecord(dir string, rec StallRecord) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, stallRecordFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

type evidenceMeta struct {
	AgentID        string    `json:"agent_id"`
	Model          string    `json:"model,omitempty"`
	ExitCode       int       `json:"exit_code"`
	Classification string    `json:"classification,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	EndedAt        time.Time `json:"ended_at,omitempty"`
	Error          string    `json:"error,omitempty"`
}

func evidenceMetaFromSnapshot(snap EvidenceSnapshot) evidenceMeta {
	meta := evidenceMeta{
		AgentID:        snap.AgentID,
		Model:          snap.Model,
		ExitCode:       snap.ExitCode,
		Classification: snap.Classification,
		StartedAt:      snap.StartedAt,
		EndedAt:        snap.EndedAt,
	}
	if snap.Err != nil {
		meta.Error = snap.Err.Error()
	}
	return meta
}

func marshalEventsJSONL(events []coreexec.Event) ([]byte, error) {
	var buf bytes.Buffer
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func assistantTextFromEvents(events []coreexec.Event) []byte {
	var buf bytes.Buffer
	first := true
	for _, event := range events {
		if event.Type != coreexec.EventStdout {
			continue
		}
		// coreexec emits stdout as bufio.Scanner line tokens, so Data excludes
		// the trailing newline. Reinsert one between stdout events to recover a
		// readable line stream without appending an extra newline at EOF.
		if !first {
			buf.WriteByte('\n')
		}
		buf.WriteString(event.Data)
		first = false
	}
	return truncateAssistantTail(buf.Bytes())
}

func truncateAssistantTail(data []byte) []byte {
	if len(data) <= assistantTextLimitBytes {
		return data
	}
	return data[len(data)-assistantTextLimitBytes:]
}

func writeEvidenceFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}
