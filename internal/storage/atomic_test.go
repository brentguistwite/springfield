package storage

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestWriteJSONAtomicNoPartialReads is the concurrency guard for the atomic
// write: enterPhase rewrites state.json on every round while `springfield status`
// reads it locklessly. A truncating os.WriteFile lets a read land mid-write and
// see partial/empty bytes that fail to unmarshal. With temp+rename, a reader sees
// either the old bytes or the new bytes — never a torn file. This test hammers
// WriteJSON (with a payload whose length varies each round, maximizing the window
// a truncating write would expose) against a concurrent ReadJSON loop and asserts
// no read ever fails to decode.
func TestWriteJSONAtomicNoPartialReads(t *testing.T) {
	rt, err := FromRoot(t.TempDir())
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	const rel = "state.json"

	type payload struct {
		Items []string `json:"items"`
	}
	build := func(n int) payload {
		items := make([]string, n)
		for i := range items {
			items[i] = strings.Repeat("x", n) // vary total length round to round
		}
		return payload{Items: items}
	}

	// Seed one write so the reader never races the very first create.
	if err := rt.WriteJSON(rel, build(1)); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	const rounds = 400
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if err := rt.WriteJSON(rel, build(1+i%50)); err != nil {
				t.Errorf("write round %d: %v", i, err)
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < rounds*4; i++ {
			var p payload
			if err := rt.ReadJSON(rel, &p); err != nil {
				// A decode failure here means the reader observed a torn write —
				// exactly what temp+rename must prevent.
				t.Errorf("read %d observed a non-atomic write: %v", i, err)
				return
			}
		}
	}()

	wg.Wait()

	// No temp scratch file should survive a successful write.
	full, err := rt.Path(rel)
	if err != nil {
		t.Fatalf("resolve path: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(full))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("leftover temp file after atomic write: %s", e.Name())
		}
	}
}
