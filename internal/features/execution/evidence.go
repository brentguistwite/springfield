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
