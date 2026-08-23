package retro_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"springfield/internal/features/retro"
)

// occ builds one Occurrence receipt with a UTC date so filename/receipt dates
// are deterministic regardless of the test host's zone.
func occ(batchID, project string, y, m, d, count int) retro.Occurrence {
	return retro.Occurrence{
		BatchID: batchID,
		Project: project,
		Count:   count,
		Date:    time.Date(y, time.Month(m), d, 12, 0, 0, 0, time.UTC),
	}
}

// sampleItem is an above-threshold item: 5 occurrences across 3 batches, oldest
// on 2026-08-10.
func sampleItem() retro.Item {
	return retro.Item{
		Key: "iteration-cap",
		Occurrences: []retro.Occurrence{
			occ("batch-c", "proj-x", 2026, 8, 14, 2),
			occ("batch-a", "proj-x", 2026, 8, 10, 2),
			occ("batch-b", "proj-y", 2026, 8, 12, 1),
		},
	}
}

func TestFile_CreateFromEmpty(t *testing.T) {
	dir := t.TempDir()
	cfg := retro.Config{ItemsDir: dir}

	res, err := cfg.File(sampleItem())
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	if !res.Created {
		t.Fatalf("first File over empty dir: Created=false, want true")
	}

	// Filename date is the OLDEST contributing retro's date, in UTC.
	wantBase := "2026-08-10-iteration-cap-auto.md"
	if got := filepath.Base(res.Path); got != wantBase {
		t.Errorf("filename = %q, want %q", got, wantBase)
	}

	body := readFile(t, res.Path)

	// Frontmatter: first item in an empty dir gets SPG-1.
	if !strings.Contains(body, "id: SPG-1") {
		t.Errorf("created file missing id SPG-1:\n%s", body)
	}
	if !strings.Contains(body, "status: todo") {
		t.Errorf("created file missing status: todo")
	}
	for _, tag := range []string{"- item", "- springfield"} {
		if !strings.Contains(body, tag) {
			t.Errorf("created file missing tag line %q", tag)
		}
	}

	// Exact required heading.
	if !strings.Contains(body, "\n## Acceptance Criteria\n") {
		t.Errorf("created file missing exact '## Acceptance Criteria' heading:\n%s", body)
	}
	// At least one checkbox that names the pattern key as an improvement task.
	if !strings.Contains(body, "- [ ]") {
		t.Errorf("created file missing a checkbox under Acceptance Criteria")
	}

	// Occurrence receipts carry every contributing batch id.
	for _, id := range []string{"batch-a", "batch-b", "batch-c"} {
		if !strings.Contains(body, id) {
			t.Errorf("created file missing occurrence receipt for %q:\n%s", id, body)
		}
	}
}

func TestFile_NeverSetsAgentReady(t *testing.T) {
	dir := t.TempDir()
	cfg := retro.Config{ItemsDir: dir}
	res, err := cfg.File(sampleItem())
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	body := readFile(t, res.Path)
	if strings.Contains(body, "agent: ready") {
		t.Errorf("created file must never set 'agent: ready':\n%s", body)
	}
}

func TestFile_GapTolerantIDAllocation(t *testing.T) {
	dir := t.TempDir()
	// Seed existing items with a gap: SPG-3 and SPG-7 present, 1/2/4/5/6 absent.
	writeItem(t, dir, "old-a.md", 3)
	writeItem(t, dir, "old-b.md", 7)

	cfg := retro.Config{ItemsDir: dir}
	res, err := cfg.File(sampleItem())
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	body := readFile(t, res.Path)
	if !strings.Contains(body, "id: SPG-8") {
		t.Errorf("expected id SPG-8 (max 7 + 1), tolerating the gap:\n%s", body)
	}
}

func TestFile_DedupUpdatePreservesStatus(t *testing.T) {
	dir := t.TempDir()
	cfg := retro.Config{ItemsDir: dir}

	res, err := cfg.File(sampleItem())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	path := res.Path

	// A human triages the ticket: flips status and edits prose.
	orig := readFile(t, path)
	edited := strings.Replace(orig, "status: todo", "status: doing", 1)
	edited = strings.Replace(edited, "_No related notes linked yet._",
		"- [[some/hand/edited/link|link]]", 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatalf("write edited: %v", err)
	}

	// A later batch trips the same pattern again: two already-seen batches plus
	// one new (batch-d). Dedup must add only batch-d.
	next := retro.Item{
		Key: "iteration-cap",
		Occurrences: []retro.Occurrence{
			occ("batch-a", "proj-x", 2026, 8, 10, 2), // already recorded
			occ("batch-b", "proj-y", 2026, 8, 12, 1), // already recorded
			occ("batch-d", "proj-z", 2026, 8, 20, 3), // new
		},
	}
	res2, err := cfg.File(next)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if res2.Created {
		t.Errorf("second File on existing item: Created=true, want false (update)")
	}
	if res2.Path != path {
		t.Errorf("stable filename per pattern broke: %q != %q", res2.Path, path)
	}

	body := readFile(t, path)
	// Human edits preserved.
	if !strings.Contains(body, "status: doing") {
		t.Errorf("update clobbered hand-edited frontmatter status:\n%s", body)
	}
	if !strings.Contains(body, "[[some/hand/edited/link|link]]") {
		t.Errorf("update clobbered hand-edited prose:\n%s", body)
	}
	// New receipt appended.
	if !strings.Contains(body, "batch-d") {
		t.Errorf("update did not append the new batch-d receipt:\n%s", body)
	}
	// Dedup: an already-seen batch id must have exactly one receipt line.
	if n := strings.Count(body, "- `batch-a`"); n != 1 {
		t.Errorf("batch-a receipt line appears %d times, want 1 (dedup failed):\n%s", n, body)
	}
}

func TestFile_ConcurrentCreateRace(t *testing.T) {
	dir := t.TempDir()
	cfg := retro.Config{ItemsDir: dir}

	const n = 8
	var wg sync.WaitGroup
	results := make([]retro.FileResult, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = cfg.File(sampleItem())
		}(i)
	}
	close(start)
	wg.Wait()

	created := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: File: %v", i, errs[i])
		}
		if results[i].Created {
			created++
		}
	}
	if created != 1 {
		t.Errorf("concurrent create: %d creates, want exactly 1 (rest updates)", created)
	}
	// Exactly one file exists on disk.
	entries, _ := os.ReadDir(dir)
	mdCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			mdCount++
		}
	}
	if mdCount != 1 {
		t.Errorf("concurrent create left %d .md files, want 1", mdCount)
	}
}

func TestFile_ConcurrentDistinctKeysGetUniqueIDs(t *testing.T) {
	dir := t.TempDir()
	cfg := retro.Config{ItemsDir: dir}

	// Each key files to its own path, so O_EXCL never contends. The hazard is id
	// allocation: nextID scans sibling files, and a racer's file may exist (via
	// O_EXCL) with no body/id written yet, so two distinct items can be handed the
	// same SPG id unless allocation is serialized.
	const n = 12
	keys := make([]string, n)
	for i := range keys {
		keys[i] = "pattern-" + itoa(i)
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			item := retro.Item{
				Key:         keys[i],
				Occurrences: []retro.Occurrence{occ("batch-a", "proj-x", 2026, 8, 10, 3)},
			}
			_, errs[i] = cfg.File(item)
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: File: %v", i, errs[i])
		}
	}

	// Every filed item must carry a distinct SPG id.
	ids := map[string]string{} // id -> file that owns it
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		body := readFile(t, filepath.Join(dir, e.Name()))
		id := extractID(t, body, e.Name())
		if prev, dup := ids[id]; dup {
			t.Errorf("duplicate id %s allocated to both %q and %q", id, prev, e.Name())
		}
		ids[id] = e.Name()
	}
	if len(ids) != n {
		t.Errorf("allocated %d distinct ids, want %d", len(ids), n)
	}
}

func TestFile_RefusesPathEscape(t *testing.T) {
	dir := t.TempDir()
	cfg := retro.Config{ItemsDir: dir}

	for _, key := range []string{"../evil", "../../etc/passwd", "sub/dir/key", "a/../../b"} {
		item := retro.Item{
			Key:         key,
			Occurrences: []retro.Occurrence{occ("batch-a", "proj-x", 2026, 8, 10, 3)},
		}
		if _, err := cfg.File(item); err == nil {
			t.Errorf("File(key=%q) = nil error, want path-escape rejection", key)
		}
	}
	// Nothing escaped the ItemsDir.
	parent := filepath.Dir(dir)
	if entries, _ := os.ReadDir(parent); len(entries) > 1 {
		// t.TempDir's parent may hold sibling temp dirs from other tests, so only
		// assert no *-auto.md leaked out beside dir.
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), "-auto.md") {
				t.Errorf("filer wrote %q outside ItemsDir", e.Name())
			}
		}
	}
}

func TestFile_DisabledWhenItemsDirEmpty(t *testing.T) {
	cfg := retro.Config{ItemsDir: ""}
	if cfg.Enabled() {
		t.Errorf("empty ItemsDir: Enabled() = true, want false")
	}
	if _, err := cfg.File(sampleItem()); err == nil {
		t.Errorf("File on disabled filer: nil error, want a disabled error")
	}
}

// --- helpers ---

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// extractID pulls the "id: SPG-<n>" value out of an item's frontmatter.
func extractID(t *testing.T, body, name string) string {
	t.Helper()
	for _, ln := range strings.Split(body, "\n") {
		if strings.HasPrefix(ln, "id: ") {
			return strings.TrimSpace(strings.TrimPrefix(ln, "id: "))
		}
	}
	t.Fatalf("file %q has no id line:\n%s", name, body)
	return ""
}

// writeItem drops a minimal convention-compliant item file carrying id SPG-<n>
// so nextID has something to scan.
func writeItem(t *testing.T, dir, name string, n int) {
	t.Helper()
	body := "---\ntype: item\nid: SPG-" +
		strings.TrimSpace(itoa(n)) +
		"\nstatus: todo\ntags:\n  - item\n  - springfield\n---\n\n# seed\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write seed item: %v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
