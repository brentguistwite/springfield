package retro

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config is the vault item filer: the actuation half of the retro loop, where an
// above-threshold [Pattern] becomes a convention-compliant ticket under an
// Obsidian project's items/ folder.
//
// ItemsDir is the vault items directory to file into (e.g.
// personal/projects/springfield/items). An empty ItemsDir disables the filer:
// [Config.Enabled] reports false and [Config.File] returns an error, so a caller
// with no vault configured skips filing entirely rather than guessing a path.
//
// The filer is a deep module. A caller hands it one [Item] and gets back a
// [FileResult]; everything about the on-disk item template (SPG id allocation,
// frontmatter schema, the required '## Acceptance Criteria' heading, the
// dedup-append occurrence log) lives behind that one call. It is deliberately
// conservative: it writes ONLY inside ItemsDir, never touches a human's
// frontmatter status or hand-edited prose on update, and never sets agent: ready
// — that flag is flipped only by human triage.
type Config struct {
	ItemsDir string
}

// Occurrence is one batch's contribution to a recurring pattern: the receipt the
// filer records so a human reading the ticket can trace it back to concrete runs.
type Occurrence struct {
	BatchID string    // the batch whose report tripped the pattern
	Date    time.Time // the batch's archived-at; all filed dates are rendered in UTC
	Project string    // the project root that contributed this occurrence
	Count   int       // findings carrying this pattern key in this batch
}

// Item packages one above-threshold pattern for filing: the classifier's stable
// key plus the per-batch receipts that justify a ticket. The caller assembles it
// from an [Aggregate] [Pattern] joined with the contributing reports' dates and
// counts.
type Item struct {
	Key         string
	Occurrences []Occurrence
}

// FileResult reports what [Config.File] did: the item's on-disk path and whether
// this call created it (true) or updated an existing ticket (false).
type FileResult struct {
	Path    string
	Created bool
}

// itemIDPrefix is the item id namespace for the springfield project (index.md's
// itemPrefix). Ids are allocated as <prefix>-<max+1>, tolerating gaps.
const itemIDPrefix = "SPG"

// backlinkPointer is the "Part of" line every filed item carries back to the
// project index, matching the vault item template.
const backlinkPointer = "> Part of [[personal/projects/springfield/index|springfield]]"

// acceptanceHeading is the exact heading the item template requires; downstream
// tooling and human triage both key on this literal string.
const acceptanceHeading = "## Acceptance Criteria"

// occurrencesHeading names the auto-managed receipt log. It is the ONLY section
// the update path rewrites; Context, Acceptance Criteria, Related, and all
// frontmatter are left byte-for-byte intact.
const occurrencesHeading = "## Occurrences"

// raceRetries bounds the create/update contention loop. A losing racer re-reads
// and takes the update path; the winner writes the whole file in one Write, so a
// few short backoffs cover the window where the file exists but is not yet
// populated.
const (
	raceRetries = 100
	raceBackoff = time.Millisecond
)

// receiptLine matches one occurrence receipt so the update path can recover the
// batch ids already logged (for dedup) and their dates/counts (for the stats
// line). Format: "- `<batch>` — <YYYY-MM-DD> — <n> occurrence(s) — <project>".
var receiptLine = regexp.MustCompile("^- `([^`]+)` — (\\d{4}-\\d{2}-\\d{2}) — (\\d+) occurrence")

// idLine matches an item's frontmatter id (e.g. "id: SPG-11") for max-id scans.
var idLine = regexp.MustCompile(`(?m)^id:\s*` + itemIDPrefix + `-(\d+)\s*$`)

// Enabled reports whether the filer has an items directory to write into. A
// caller checks this and skips filing when false.
func (c Config) Enabled() bool { return strings.TrimSpace(c.ItemsDir) != "" }

// File creates or updates the vault ticket for one above-threshold item under
// ItemsDir and reports what it did.
//
// The target filename is <ItemsDir>/YYYY-MM-DD-<key>-auto.md, where the date is
// the OLDEST contributing occurrence's date in UTC. This stable-per-pattern name
// is the dedup key: a pattern always maps to the same file, so a later batch that
// trips the same key updates that ticket instead of spawning a new one.
//
// Create mirrors the vault item template: frontmatter (type: item, a freshly
// allocated SPG id, status: todo, tags), the project backlink pointer, a
// humanized H1, a '## Context' with concrete receipts, the required
// '## Acceptance Criteria' heading carrying an improvement checkbox, an
// '## Occurrences' receipt log, and a '## Related' placeholder. Update appends
// only new occurrence receipts (dedup by batch id) under '## Occurrences' and
// refreshes that section's stats line, never touching frontmatter status or
// hand-edited prose.
//
// Create uses O_EXCL: if a concurrent call wins the create race, this one re-reads
// and takes the update path, so concurrent Files over the same item resolve to
// exactly one create plus updates. File writes ONLY inside ItemsDir; a key that
// would escape it is rejected.
func (c Config) File(item Item) (FileResult, error) {
	if !c.Enabled() {
		return FileResult{}, errors.New("retro: filer disabled (empty ItemsDir)")
	}
	key := strings.TrimSpace(item.Key)
	if key == "" {
		return FileResult{}, errors.New("retro: item key must not be empty")
	}
	if len(item.Occurrences) == 0 {
		return FileResult{}, fmt.Errorf("retro: item %q has no occurrences", key)
	}

	path, err := c.itemPath(key, oldestDate(item.Occurrences))
	if err != nil {
		return FileResult{}, err
	}
	if err := os.MkdirAll(c.ItemsDir, 0o755); err != nil {
		return FileResult{}, fmt.Errorf("retro: create items dir: %w", err)
	}

	for attempt := 0; attempt < raceRetries; attempt++ {
		data, rerr := os.ReadFile(path)
		switch {
		case rerr == nil:
			if len(strings.TrimSpace(string(data))) == 0 {
				// The file exists but is empty: a racer created it via O_EXCL and has
				// not written its body yet. Wait and re-read rather than update a shell.
				time.Sleep(raceBackoff)
				continue
			}
			return c.update(path, string(data), item)
		case errors.Is(rerr, os.ErrNotExist):
			res, cerr := c.create(path, item)
			if errors.Is(cerr, os.ErrExist) {
				continue // lost the create race; loop back and take the update path
			}
			if cerr != nil {
				return FileResult{}, cerr
			}
			return res, nil
		default:
			return FileResult{}, fmt.Errorf("retro: read item %s: %w", path, rerr)
		}
	}
	return FileResult{}, fmt.Errorf("retro: gave up filing %q after %d contended attempts", key, raceRetries)
}

// itemPath builds the stable ticket path for a key and rejects any key that would
// place the file outside ItemsDir (path traversal via "/" or ".." in the key).
func (c Config) itemPath(key string, oldest time.Time) (string, error) {
	base := fmt.Sprintf("%s-%s-auto.md", oldest.UTC().Format("2006-01-02"), key)
	full := filepath.Join(c.ItemsDir, base)
	if filepath.Dir(full) != filepath.Clean(c.ItemsDir) {
		return "", fmt.Errorf("retro: item key %q would escape ItemsDir", key)
	}
	return full, nil
}

// create writes a brand-new ticket via O_EXCL so exactly one concurrent caller
// wins. On a create race the loser sees os.ErrExist here and falls back to update.
func (c Config) create(path string, item Item) (FileResult, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return FileResult{}, err // may be os.ErrExist — the caller detects and updates
	}
	id, err := c.nextID()
	if err != nil {
		f.Close()
		_ = os.Remove(path)
		return FileResult{}, err
	}
	body := renderCreate(item, id)
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		_ = os.Remove(path)
		return FileResult{}, fmt.Errorf("retro: write item %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return FileResult{}, fmt.Errorf("retro: fsync item %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return FileResult{}, fmt.Errorf("retro: close item %s: %w", path, err)
	}
	return FileResult{Path: path, Created: true}, nil
}

// update rewrites only the Occurrences section, merging new receipts (dedup by
// batch id) and refreshing the stats line, then writes atomically. Frontmatter
// and every hand-editable prose section are preserved byte-for-byte.
func (c Config) update(path, existing string, item Item) (FileResult, error) {
	merged := mergeOccurrences(existing, item.Occurrences)
	if merged != existing {
		if err := writeFileAtomic(path, []byte(merged), 0o644); err != nil {
			return FileResult{}, err
		}
	}
	return FileResult{Path: path, Created: false}, nil
}

// nextID scans ItemsDir for the maximum SPG-<n> in existing items' frontmatter
// and returns <prefix>-<max+1>. Gaps are tolerated: allocation keys off the max,
// not a dense sequence, so a deleted item never causes an id collision.
func (c Config) nextID() (string, error) {
	entries, err := os.ReadDir(c.ItemsDir)
	if err != nil {
		return "", fmt.Errorf("retro: scan items dir: %w", err)
	}
	max := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(c.ItemsDir, e.Name()))
		if err != nil {
			continue // an unreadable sibling never blocks allocation
		}
		if m := idLine.FindStringSubmatch(string(data)); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > max {
				max = n
			}
		}
	}
	return fmt.Sprintf("%s-%d", itemIDPrefix, max+1), nil
}

// receipt is one parsed occurrence log line, the structured form the update path
// merges and re-renders.
type receipt struct {
	batchID string
	date    time.Time
	count   int
	project string
}

func receiptOf(o Occurrence) receipt {
	return receipt{batchID: o.BatchID, date: o.Date.UTC(), count: o.Count, project: o.Project}
}

// renderCreate builds a full item file mirroring the vault item template.
func renderCreate(item Item, id string) string {
	receipts := dedupReceipts(nil, item.Occurrences)

	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("type: item\n")
	b.WriteString("id: " + id + "\n")
	b.WriteString("status: todo\n")
	b.WriteString("blockedBy: []\n")
	b.WriteString("tags:\n  - item\n  - springfield\n")
	b.WriteString("---\n\n")
	b.WriteString(backlinkPointer + "\n\n")
	b.WriteString("# " + humanizeKey(item.Key) + "\n\n")

	b.WriteString("## Context\n\n")
	b.WriteString(fmt.Sprintf(
		"Springfield's retrospective aggregator flagged `%s` as a recurring failure "+
			"mode: it cleared the filing threshold of >= %d occurrences across >= %d "+
			"distinct batches. Contributing batches: %s.\n\n",
		item.Key, MinOccurrences, MinBatches, contextReceipts(receipts)))

	b.WriteString(acceptanceHeading + "\n\n")
	b.WriteString(fmt.Sprintf(
		"- [ ] Eliminate the recurring `%s` failure mode at its source so batches "+
			"stop tripping it.\n\n", item.Key))

	b.WriteString(renderOccurrences(receipts))
	b.WriteString("\n## Related\n\n_No related notes linked yet._\n")
	return b.String()
}

// mergeOccurrences splices a refreshed Occurrences section into an existing file,
// preserving everything outside that section. New receipts are appended after the
// ones already logged (dedup by batch id); if the file has no Occurrences section
// yet, one is appended at the end.
func mergeOccurrences(existing string, occ []Occurrence) string {
	lines := strings.Split(existing, "\n")
	start := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == occurrencesHeading {
			start = i
			break
		}
	}
	if start == -1 {
		trimmed := strings.TrimRight(existing, "\n")
		return trimmed + "\n\n" + renderOccurrences(dedupReceipts(nil, occ))
	}

	// The section runs from its heading to the next '## ' heading (or EOF).
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}

	existingReceipts := parseReceipts(lines[start+1 : end])
	merged := dedupReceipts(existingReceipts, occ)

	// Rebuild: everything before the section, the refreshed section, everything
	// after. renderOccurrences ends with a trailing newline, so trim the split
	// artifact and rejoin cleanly.
	var out strings.Builder
	for _, ln := range lines[:start] {
		out.WriteString(ln + "\n")
	}
	out.WriteString(strings.TrimRight(renderOccurrences(merged), "\n"))
	if end < len(lines) {
		out.WriteString("\n\n")
		out.WriteString(strings.Join(lines[end:], "\n"))
	} else {
		out.WriteString("\n")
	}
	return out.String()
}

// parseReceipts recovers structured receipts from the receipt lines of an
// existing Occurrences section, ignoring the stats line and blanks.
func parseReceipts(sectionLines []string) []receipt {
	var out []receipt
	for _, ln := range sectionLines {
		m := receiptLine.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		d, err := time.Parse("2006-01-02", m[2])
		if err != nil {
			continue
		}
		n, _ := strconv.Atoi(m[3])
		out = append(out, receipt{batchID: m[1], date: d.UTC(), count: n, project: projectOf(ln)})
	}
	return out
}

// projectOf recovers the trailing "— <project>" of a receipt line, if present.
func projectOf(line string) string {
	parts := strings.Split(line, " — ")
	if len(parts) >= 4 {
		return strings.TrimSpace(parts[len(parts)-1])
	}
	return ""
}

// dedupReceipts merges new occurrences into an existing (ordered) receipt list,
// dropping any whose batch id is already logged. Existing order is preserved;
// genuinely new receipts are appended sorted by date then batch id for stable
// output.
func dedupReceipts(existing []receipt, occ []Occurrence) []receipt {
	seen := map[string]struct{}{}
	out := make([]receipt, 0, len(existing)+len(occ))
	for _, r := range existing {
		if _, ok := seen[r.batchID]; ok {
			continue
		}
		seen[r.batchID] = struct{}{}
		out = append(out, r)
	}
	fresh := make([]receipt, 0, len(occ))
	for _, o := range occ {
		if o.BatchID == "" {
			continue
		}
		if _, ok := seen[o.BatchID]; ok {
			continue
		}
		seen[o.BatchID] = struct{}{}
		fresh = append(fresh, receiptOf(o))
	}
	sort.Slice(fresh, func(i, j int) bool {
		if !fresh[i].date.Equal(fresh[j].date) {
			return fresh[i].date.Before(fresh[j].date)
		}
		return fresh[i].batchID < fresh[j].batchID
	})
	return append(out, fresh...)
}

// renderOccurrences renders the Occurrences section: heading, a refreshed stats
// line, then one receipt line per batch. It always ends with a trailing newline.
func renderOccurrences(receipts []receipt) string {
	var b strings.Builder
	b.WriteString(occurrencesHeading + "\n\n")
	b.WriteString(statsLine(receipts) + "\n\n")
	for _, r := range receipts {
		line := fmt.Sprintf("- `%s` — %s — %d occurrence(s)", r.batchID, r.date.UTC().Format("2006-01-02"), r.count)
		if r.project != "" {
			line += " — " + r.project
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// statsLine summarizes the merged receipts: total occurrences, distinct batches,
// and the most recent date. It is regenerated on every update.
func statsLine(receipts []receipt) string {
	totalOcc := 0
	var last time.Time
	for _, r := range receipts {
		totalOcc += r.count
		if r.date.After(last) {
			last = r.date
		}
	}
	seen := "—"
	if !last.IsZero() {
		seen = last.UTC().Format("2006-01-02")
	}
	return fmt.Sprintf("> %d occurrence(s) across %d batch(es); last seen %s.",
		totalOcc, len(receipts), seen)
}

// contextReceipts renders the human-readable batch/date/project list for the
// Context section.
func contextReceipts(receipts []receipt) string {
	parts := make([]string, 0, len(receipts))
	for _, r := range receipts {
		if r.project != "" {
			parts = append(parts, fmt.Sprintf("`%s` (%s, %s)", r.batchID, r.date.UTC().Format("2006-01-02"), r.project))
		} else {
			parts = append(parts, fmt.Sprintf("`%s` (%s)", r.batchID, r.date.UTC().Format("2006-01-02")))
		}
	}
	return strings.Join(parts, ", ")
}

// oldestDate returns the earliest occurrence date, which anchors the stable
// filename.
func oldestDate(occ []Occurrence) time.Time {
	oldest := occ[0].Date
	for _, o := range occ[1:] {
		if o.Date.Before(oldest) {
			oldest = o.Date
		}
	}
	return oldest
}

// humanizeKey turns a machine pattern key ("iteration-cap") into a title-cased
// H1 ("Iteration Cap").
func humanizeKey(key string) string {
	words := strings.FieldsFunc(key, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}
