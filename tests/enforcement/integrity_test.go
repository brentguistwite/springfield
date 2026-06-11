// Package enforcement_test holds the always-on gates that keep Springfield's
// transport parsers honest: the real-capture corpus is verbatim + well-formed
// (this file) and every transport parser is exercised against it (coverage).
//
// These encode the lesson from the 2026-06-03 dogfood gate bugs: a parser of
// another tool's stream-json output must be tested against bytes the real CLI
// actually emits, not a hand-authored struct. Authenticity (real-binary origin)
// is NOT machine-provable offline — the corpus is captured via
// cmd/capture-fixture and vouched for by tool_version + review; what these
// gates DO prove is producer-shape, immutability-after-commit, and coverage.
package enforcement_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const realcapturesDir = "../realcaptures"

type captureMeta struct {
	Tool        string   `json:"tool"`
	ToolVersion string   `json:"tool_version"`
	Scenario    string   `json:"scenario"`
	CommandArgs []string `json:"command_args"`
	SHA256      string   `json:"sha256"`
}

// jsonlFixtures returns every <tool>/<scenario>.jsonl under the corpus.
func jsonlFixtures(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(realcapturesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no real-capture fixtures found — the corpus must not be empty")
	}
	return out
}

// TestRealcapturesVerbatimAndWellFormed pins that every captured fixture is
// (1) byte-identical to its recorded sha256 (immutability — a hand-edit to
// fake a transcript is caught), (2) newline-delimited valid JSON (the shape the
// production reader splits), and (3) paired with a well-formed sibling meta.
func TestRealcapturesVerbatimAndWellFormed(t *testing.T) {
	for _, jsonl := range jsonlFixtures(t) {
		jsonl := jsonl
		t.Run(jsonl, func(t *testing.T) {
			raw, err := os.ReadFile(jsonl)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			metaPath := strings.TrimSuffix(jsonl, ".jsonl") + ".meta.json"
			mraw, err := os.ReadFile(metaPath)
			if err != nil {
				t.Fatalf("every fixture needs a sibling .meta.json: %v", err)
			}
			var meta captureMeta
			if err := json.Unmarshal(mraw, &meta); err != nil {
				t.Fatalf("meta is not valid JSON: %v", err)
			}
			if meta.Tool == "" || meta.ToolVersion == "" || meta.SHA256 == "" {
				t.Fatalf("meta missing required fields (tool/tool_version/sha256): %+v", meta)
			}

			got := fmt.Sprintf("%x", sha256.Sum256(raw))
			if got != meta.SHA256 {
				t.Fatalf("fixture drifted from its recorded sha256 (hand-edit?):\n  got  %s\n  meta %s\nrecapture via cmd/capture-fixture instead of editing", got, meta.SHA256)
			}

			for i, line := range strings.Split(string(raw), "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}
				if !json.Valid([]byte(line)) {
					t.Fatalf("line %d is not valid JSON (corpus must be newline-delimited JSON): %q", i+1, line)
				}
			}
		})
	}
}

// TestRealcapturesNoOrphanMeta pins that every .meta.json has its .jsonl, so a
// fixture can't be deleted while leaving a dangling provenance record.
func TestRealcapturesNoOrphanMeta(t *testing.T) {
	err := filepath.WalkDir(realcapturesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".meta.json") {
			return nil
		}
		jsonl := strings.TrimSuffix(path, ".meta.json") + ".jsonl"
		if _, statErr := os.Stat(jsonl); statErr != nil {
			t.Errorf("orphan meta with no fixture: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
}
