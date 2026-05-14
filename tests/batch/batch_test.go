package batch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/features/batch"
	"springfield/internal/features/prd"
)

// --- sanitize ---

func TestSanitizeID(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Hello World", "hello-world"},
		{"Feat: Add OAuth 2.0", "feat-add-oauth-2-0"},
		{"  --leading-trailing--  ", "leading-trailing"},
		{"UPPER_CASE", "upper-case"},
		{"a", "a"},
		{"", ""},
	}
	for _, c := range cases {
		got := batch.SanitizeID(c.in)
		if got != c.want {
			t.Errorf("SanitizeID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUniqueID(t *testing.T) {
	existing := map[string]struct{}{"foo": {}, "foo-2": {}}
	if got := batch.UniqueID("foo", existing); got != "foo-3" {
		t.Errorf("UniqueID = %q, want foo-3", got)
	}
	if got := batch.UniqueID("bar", existing); got != "bar" {
		t.Errorf("UniqueID = %q, want bar", got)
	}
}

// --- compile (PRD-based) ---

func makeEnvelope(title string, phases []prd.PhasePRD, plans []prd.BatchPRDPlan) prd.BatchPRDEnvelope {
	return prd.BatchPRDEnvelope{
		Title:  title,
		Source: "test source",
		Phases: phases,
		Plans:  plans,
	}
}

// minStory returns a minimal valid user story for test envelopes.
func minStory() prd.UserStory {
	return prd.UserStory{
		ID:                 "US-001",
		Title:              "placeholder",
		Priority:           1,
		AcceptanceCriteria: []string{"passes"},
	}
}

func makePlan(id, ptitle string) prd.BatchPRDPlan {
	return prd.BatchPRDPlan{
		PRD: prd.PRD{
			ID:          id,
			Title:       ptitle,
			UserStories: []prd.UserStory{minStory()},
		},
	}
}

func makePlanWithContext(id, ptitle, ctx string) prd.BatchPRDPlan {
	return prd.BatchPRDPlan{
		PRD: prd.PRD{
			ID:          id,
			Title:       ptitle,
			UserStories: []prd.UserStory{minStory()},
		},
		ContextMD: ctx,
	}
}

// TestCompile_PlanIDsDeduplication: envelope with phases [{plans:[a,b]},{plans:[b,c]}]
// → PlanIDs = [a, b, c] in first-seen order.
func TestCompile_PlanIDsDeduplication(t *testing.T) {
	env := makeEnvelope("dedup test",
		[]prd.PhasePRD{
			{Mode: "serial", Plans: []string{"a", "b"}},
			{Mode: "serial", Plans: []string{"b", "c"}},
		},
		[]prd.BatchPRDPlan{
			makePlan("a", "Plan A"),
			makePlan("b", "Plan B"),
			makePlan("c", "Plan C"),
		},
	)
	out, err := batch.Compile(batch.CompileInput{
		Envelope: env,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	want := []string{"a", "b", "c"}
	if len(out.Batch.PlanIDs) != len(want) {
		t.Fatalf("PlanIDs = %v, want %v", out.Batch.PlanIDs, want)
	}
	for i, id := range want {
		if out.Batch.PlanIDs[i] != id {
			t.Errorf("PlanIDs[%d] = %q, want %q", i, out.Batch.PlanIDs[i], id)
		}
	}
}

// TestCompile_PlanIDsFirstSeenOrder: duplicate in second phase is not re-appended.
func TestCompile_PlanIDsFirstSeenOrder(t *testing.T) {
	env := makeEnvelope("order test",
		[]prd.PhasePRD{
			{Mode: "serial", Plans: []string{"c", "a"}},
			{Mode: "serial", Plans: []string{"a", "b"}},
		},
		[]prd.BatchPRDPlan{
			makePlan("c", "Plan C"),
			makePlan("a", "Plan A"),
			makePlan("b", "Plan B"),
		},
	)
	out, err := batch.Compile(batch.CompileInput{Envelope: env})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// first-seen order: c, a, b
	want := []string{"c", "a", "b"}
	if len(out.Batch.PlanIDs) != 3 {
		t.Fatalf("PlanIDs = %v, want %v", out.Batch.PlanIDs, want)
	}
	for i, id := range want {
		if out.Batch.PlanIDs[i] != id {
			t.Errorf("PlanIDs[%d] = %q, want %q", i, out.Batch.PlanIDs[i], id)
		}
	}
}

// TestCompile_BuildsBatchShape: verify Batch fields are populated correctly.
func TestCompile_BuildsBatchShape(t *testing.T) {
	env := makeEnvelope("My Feature",
		[]prd.PhasePRD{
			{Mode: "serial", Plans: []string{"plan-a"}},
		},
		[]prd.BatchPRDPlan{
			makePlan("plan-a", "Plan A"),
		},
	)
	out, err := batch.Compile(batch.CompileInput{Envelope: env})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if out.Batch.ID == "" {
		t.Error("batch ID must not be empty")
	}
	if out.Batch.Title != "My Feature" {
		t.Errorf("Title = %q, want My Feature", out.Batch.Title)
	}
	if len(out.Batch.Phases) != 1 {
		t.Fatalf("phase count = %d, want 1", len(out.Batch.Phases))
	}
	if len(out.Batch.Phases[0].Plans) != 1 || out.Batch.Phases[0].Plans[0] != "plan-a" {
		t.Errorf("phase plans = %v, want [plan-a]", out.Batch.Phases[0].Plans)
	}
}

// TestCompile_BatchIDSanitized: title → slug.
func TestCompile_BatchIDSanitized(t *testing.T) {
	env := makeEnvelope("Add OAuth 2.0!",
		[]prd.PhasePRD{{Plans: []string{"p"}}},
		[]prd.BatchPRDPlan{makePlan("p", "P")},
	)
	out, err := batch.Compile(batch.CompileInput{Envelope: env})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for _, ch := range out.Batch.ID {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
			t.Errorf("batch ID %q contains unsafe char %q", out.Batch.ID, string(ch))
		}
	}
}

// TestCompile_BatchIDDeduplication: existing ID → suffix appended.
func TestCompile_BatchIDDeduplication(t *testing.T) {
	env := makeEnvelope("scaffold",
		[]prd.PhasePRD{{Plans: []string{"p"}}},
		[]prd.BatchPRDPlan{makePlan("p", "P")},
	)
	out, err := batch.Compile(batch.CompileInput{
		Envelope:    env,
		ExistingIDs: map[string]struct{}{"scaffold": {}},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if out.Batch.ID != "scaffold-2" {
		t.Errorf("batch ID = %q, want scaffold-2", out.Batch.ID)
	}
}

// TestCompile_WrittenPlansContainPRDBytes: each plan serialized to prd.json bytes.
func TestCompile_WrittenPlansContainPRDBytes(t *testing.T) {
	env := makeEnvelope("prd test",
		[]prd.PhasePRD{{Plans: []string{"plan-a", "plan-b"}}},
		[]prd.BatchPRDPlan{
			makePlan("plan-a", "Plan A"),
			makePlan("plan-b", "Plan B"),
		},
	)
	out, err := batch.Compile(batch.CompileInput{Envelope: env})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(out.Plans) != 2 {
		t.Fatalf("Plans count = %d, want 2", len(out.Plans))
	}
	byID := map[string]batch.WrittenPlan{}
	for _, wp := range out.Plans {
		byID[wp.ID] = wp
	}
	for _, id := range []string{"plan-a", "plan-b"} {
		wp, ok := byID[id]
		if !ok {
			t.Errorf("missing WrittenPlan for %q", id)
			continue
		}
		if len(wp.PRDBytes) == 0 {
			t.Errorf("plan %q: PRDBytes empty", id)
		}
		var decoded prd.PRD
		if err := json.Unmarshal(wp.PRDBytes, &decoded); err != nil {
			t.Errorf("plan %q: PRDBytes not valid JSON: %v", id, err)
		}
		if decoded.ID != id {
			t.Errorf("plan %q: decoded ID = %q", id, decoded.ID)
		}
	}
}

// TestCompile_ContextBytesFromEnvelope: context_md captured per plan.
func TestCompile_ContextBytesFromEnvelope(t *testing.T) {
	env := makeEnvelope("ctx test",
		[]prd.PhasePRD{{Plans: []string{"plan-a", "plan-b"}}},
		[]prd.BatchPRDPlan{
			makePlanWithContext("plan-a", "Plan A", "## context for A"),
			makePlan("plan-b", "Plan B"), // no context
		},
	)
	out, err := batch.Compile(batch.CompileInput{Envelope: env})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	byID := map[string]batch.WrittenPlan{}
	for _, wp := range out.Plans {
		byID[wp.ID] = wp
	}
	if string(byID["plan-a"].ContextBytes) != "## context for A" {
		t.Errorf("plan-a ContextBytes = %q, want '## context for A'", byID["plan-a"].ContextBytes)
	}
	if len(byID["plan-b"].ContextBytes) != 0 {
		t.Errorf("plan-b ContextBytes should be empty, got %q", byID["plan-b"].ContextBytes)
	}
}

// TestCompile_PlanUnitOrderMatchesFirstAppearance: PlanUnit.Order = 1-based first-phase-appearance index.
func TestCompile_PlanUnitOrderMatchesFirstAppearance(t *testing.T) {
	env := makeEnvelope("order test",
		[]prd.PhasePRD{
			{Plans: []string{"b", "a"}}, // b=1, a=2
			{Plans: []string{"a", "c"}}, // a already seen; c=3
		},
		[]prd.BatchPRDPlan{
			makePlan("a", "Plan A"),
			makePlan("b", "Plan B"),
			makePlan("c", "Plan C"),
		},
	)
	out, err := batch.Compile(batch.CompileInput{Envelope: env})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(out.Units) != 3 {
		t.Fatalf("Units count = %d, want 3", len(out.Units))
	}
	byID := map[string]int{}
	for _, u := range out.Units {
		byID[u.ID] = u.Order
	}
	if byID["b"] != 1 {
		t.Errorf("b order = %d, want 1", byID["b"])
	}
	if byID["a"] != 2 {
		t.Errorf("a order = %d, want 2", byID["a"])
	}
	if byID["c"] != 3 {
		t.Errorf("c order = %d, want 3", byID["c"])
	}
}

// TestCompile_PlanUnitPathIsPRDJSON: Path = .springfield/plans/<id>/prd.json
func TestCompile_PlanUnitPathIsPRDJSON(t *testing.T) {
	env := makeEnvelope("path test",
		[]prd.PhasePRD{{Plans: []string{"my-plan"}}},
		[]prd.BatchPRDPlan{makePlan("my-plan", "My Plan")},
	)
	out, err := batch.Compile(batch.CompileInput{Envelope: env})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(out.Units) != 1 {
		t.Fatalf("Units count = %d, want 1", len(out.Units))
	}
	u := out.Units[0]
	if u.Path != ".springfield/plans/my-plan/prd.json" {
		t.Errorf("Path = %q, want .springfield/plans/my-plan/prd.json", u.Path)
	}
}

// TestCompile_EmptyEnvelopeTitle: empty title returns error.
func TestCompile_EmptyEnvelopeTitle(t *testing.T) {
	env := makeEnvelope("",
		[]prd.PhasePRD{{Plans: []string{"p"}}},
		[]prd.BatchPRDPlan{makePlan("p", "P")},
	)
	_, err := batch.Compile(batch.CompileInput{Envelope: env})
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

// TestCompile_PhaseReferencesUnknownPlanReturnsError: phase referencing a plan
// ID not present in the Plans list must return an error (not silently drop it).
func TestCompile_PhaseReferencesUnknownPlanReturnsError(t *testing.T) {
	env := makeEnvelope("unknown ref",
		[]prd.PhasePRD{
			{Mode: "serial", Plans: []string{"known", "ghost"}},
		},
		[]prd.BatchPRDPlan{
			makePlan("known", "Known Plan"),
			// "ghost" is referenced in phase but missing from plans list
		},
	)
	_, err := batch.Compile(batch.CompileInput{Envelope: env})
	if err == nil {
		t.Fatal("expected error when phase references unknown plan ID")
	}
}

// TestCompile_EmptyPlansReturnsError: envelope with no plans returns error.
func TestCompile_EmptyPlansReturnsError(t *testing.T) {
	env := makeEnvelope("test",
		[]prd.PhasePRD{},
		[]prd.BatchPRDPlan{},
	)
	_, err := batch.Compile(batch.CompileInput{Envelope: env})
	if err == nil {
		t.Fatal("expected error for empty plans")
	}
}

// --- storage ---

func TestWriteAndReadBatch(t *testing.T) {
	dir := t.TempDir()
	paths, err := batch.NewPaths(dir, "my-batch")
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}

	b := batch.Batch{
		ID:      "my-batch",
		Title:   "My Batch",
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"01"}}},
		PlanIDs: []string{"01"},
	}
	plans := []batch.WrittenPlan{
		{
			ID:       "01",
			PRDBytes: []byte(`{"id":"01","title":"Plan 01","description":"","tags":null,"user_stories":null}`),
		},
	}

	if err := batch.WriteBatch(paths, b, "do stuff", plans); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	got, err := batch.ReadBatch(paths)
	if err != nil {
		t.Fatalf("ReadBatch: %v", err)
	}
	if got.ID != "my-batch" {
		t.Errorf("batch ID = %q, want my-batch", got.ID)
	}
	if len(got.PlanIDs) != 1 {
		t.Fatalf("plan id count = %d, want 1", len(got.PlanIDs))
	}

	sourcePath := paths.SourcePath()
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if string(data) != "do stuff" {
		t.Errorf("source = %q, want 'do stuff'", string(data))
	}

	// prd.json must be written for plan 01
	prdPath := batch.PlanPRDPath(dir, "01")
	if _, err := os.Stat(prdPath); err != nil {
		t.Errorf("prd.json not written for plan 01: %v", err)
	}
}

func TestWriteBatch_ContextMDWrittenWhenNonEmpty(t *testing.T) {
	dir := t.TempDir()
	paths, _ := batch.NewPaths(dir, "ctx-batch")
	b := batch.Batch{
		ID:      "ctx-batch",
		Title:   "Ctx",
		PlanIDs: []string{"plan-a"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"plan-a"}}},
	}
	plans := []batch.WrittenPlan{
		{
			ID:           "plan-a",
			PRDBytes:     []byte(`{"id":"plan-a","title":"A","description":"","tags":null,"user_stories":null}`),
			ContextBytes: []byte("## context for A"),
		},
	}
	if err := batch.WriteBatch(paths, b, "src", plans); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	ctxPath := batch.PlanContextPath(dir, "plan-a")
	data, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("context.md not written: %v", err)
	}
	if string(data) != "## context for A" {
		t.Errorf("context.md = %q, want '## context for A'", string(data))
	}
}

func TestWriteBatch_ContextMDNotWrittenWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	paths, _ := batch.NewPaths(dir, "no-ctx-batch")
	b := batch.Batch{
		ID:      "no-ctx-batch",
		Title:   "NoCtx",
		PlanIDs: []string{"plan-a"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"plan-a"}}},
	}
	plans := []batch.WrittenPlan{
		{
			ID:       "plan-a",
			PRDBytes: []byte(`{"id":"plan-a","title":"A","description":"","tags":null,"user_stories":null}`),
		},
	}
	if err := batch.WriteBatch(paths, b, "src", plans); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	ctxPath := batch.PlanContextPath(dir, "plan-a")
	if _, err := os.Stat(ctxPath); !os.IsNotExist(err) {
		t.Error("context.md should not be written when ContextBytes is empty")
	}
}

func TestWriteAndReadRun(t *testing.T) {
	dir := t.TempDir()

	r := batch.Run{
		ActiveBatchID:  "my-batch",
		LastCheckpoint: time.Now().UTC().Truncate(time.Second),
	}
	if err := batch.WriteRun(dir, r); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	got, ok, err := batch.ReadRun(dir)
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if !ok {
		t.Fatal("ReadRun: expected ok=true")
	}
	if got.ActiveBatchID != "my-batch" {
		t.Errorf("ActiveBatchID = %q, want my-batch", got.ActiveBatchID)
	}
}

func TestReadRunMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, ok, err := batch.ReadRun(dir)
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if ok {
		t.Error("expected ok=false for missing run.json")
	}
}

func TestArchiveBatchNormalized(t *testing.T) {
	dir := t.TempDir()
	paths, _ := batch.NewPaths(dir, "my-batch")
	b := batch.Batch{
		ID:      "my-batch",
		Title:   "My Batch",
		PlanIDs: []string{"01"},
	}
	if err := batch.WriteBatch(paths, b, "source", []batch.WrittenPlan{
		{ID: "01", PRDBytes: []byte(`{"id":"01","title":"T","description":"","tags":null,"user_stories":null}`)},
	}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	if err := batch.ArchiveBatchNormalized(dir, b, "replaced"); err != nil {
		t.Fatalf("ArchiveBatchNormalized: %v", err)
	}

	if _, err := os.Stat(paths.PlanDir()); !os.IsNotExist(err) {
		t.Error("plan dir should be removed after archive")
	}

	archiveDir := batch.ArchiveDir(dir)
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("read archive dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected archive entry to exist")
	}
}

// --- paths ---

func TestPlanDirByID(t *testing.T) {
	got := batch.PlanDirByID("/root", "my-plan")
	if got != "/root/.springfield/plans/my-plan" {
		t.Errorf("PlanDirByID = %q", got)
	}
}

func TestPlanPRDPath(t *testing.T) {
	got := batch.PlanPRDPath("/root", "my-plan")
	if got != "/root/.springfield/plans/my-plan/prd.json" {
		t.Errorf("PlanPRDPath = %q", got)
	}
}

func TestPlanContextPath(t *testing.T) {
	got := batch.PlanContextPath("/root", "my-plan")
	if got != "/root/.springfield/plans/my-plan/context.md" {
		t.Errorf("PlanContextPath = %q", got)
	}
}

func TestPlanProgressPath(t *testing.T) {
	got := batch.PlanProgressPath("/root", "my-plan")
	if got != "/root/.springfield/plans/my-plan/progress.md" {
		t.Errorf("PlanProgressPath = %q", got)
	}
}

// --- archive ---

func TestArchiveBatchNormalizedConvertsNonTerminalPlansToAborted(t *testing.T) {
	dir := t.TempDir()
	paths, _ := batch.NewPaths(dir, "b")
	b := batch.Batch{
		ID:      "b",
		Title:   "B",
		PlanIDs: []string{"01"},
	}
	if err := batch.WriteBatch(paths, b, "", []batch.WrittenPlan{
		{ID: "01", PRDBytes: []byte(`{"id":"01","title":"T","description":"","tags":null,"user_stories":null}`)},
	}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if err := batch.ArchiveBatchNormalized(dir, b, "completed"); err != nil {
		t.Fatalf("ArchiveBatchNormalized: %v", err)
	}

	entries, _ := os.ReadDir(batch.ArchiveDir(dir))
	if len(entries) != 1 {
		t.Fatalf("archive entries = %d, want 1", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(batch.ArchiveDir(dir), entries[0].Name()))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	var got batch.ArchiveEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Phase 2: ArchiveBatchNormalized stores batch metadata only; plan status
	// tracking is not yet wired, so Plans is empty.
	if len(got.Plans) != 0 {
		t.Errorf("expected archive Plans to be empty (no status tracking yet), got %d entries", len(got.Plans))
	}
}

// TestWriteBatchRollbackOnPartialFailure verifies that WriteBatch removes the
// first plan's dir when writing the second plan's dir fails.
func TestWriteBatchRollbackOnPartialFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file-permission test does not apply when running as root")
	}
	dir := t.TempDir()
	paths, err := batch.NewPaths(dir, "rollback-batch")
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}

	b := batch.Batch{
		ID:      "rollback-batch",
		Title:   "Rollback Test",
		PlanIDs: []string{"plan-first", "plan-second"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"plan-first", "plan-second"}}},
	}

	// Pre-create the plans parent dir, then make it read-only. After plan-first
	// is written, WriteBatch will attempt RemoveAll+MkdirAll for plan-second, but
	// the read-only parent blocks creating the plan-second directory.
	plansParent := filepath.Join(dir, ".springfield", "plans")
	if err := os.MkdirAll(plansParent, 0o755); err != nil {
		t.Fatalf("mkdirall plans parent: %v", err)
	}

	plans := []batch.WrittenPlan{
		{
			ID:       "plan-first",
			PRDBytes: []byte(`{"id":"plan-first","title":"First","description":"","tags":null,"user_stories":null}`),
		},
		{
			ID:       "plan-second",
			PRDBytes: []byte(`{"id":"plan-second","title":"Second","description":"","tags":null,"user_stories":null}`),
		},
	}

	// Make the plans parent read-only before running WriteBatch.
	if err := os.Chmod(plansParent, 0o555); err != nil {
		t.Fatalf("chmod plans parent: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(plansParent, 0o755) })

	err = batch.WriteBatch(paths, b, "src", plans)
	// Restore permissions before assertions so cleanup works.
	_ = os.Chmod(plansParent, 0o755)
	if err == nil {
		t.Fatal("expected WriteBatch to fail when per-plan dir creation fails")
	}

	// Rollback: first plan's dir should have been removed.
	firstPlanDir := batch.PlanDirByID(dir, "plan-first")
	if _, statErr := os.Stat(firstPlanDir); !os.IsNotExist(statErr) {
		t.Errorf("rollback failed: first plan dir still exists at %s", firstPlanDir)
	}
}

// TestWriteFileAtomicIsCrashSafe — after a successful write the target exists
// and no stray .tmp files remain alongside it.
func TestWriteFileAtomicIsCrashSafe(t *testing.T) {
	dir := t.TempDir()
	paths, _ := batch.NewPaths(dir, "b")
	b := batch.Batch{ID: "b", Title: "B", PlanIDs: []string{}}
	if err := batch.WriteBatch(paths, b, "src", nil); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	entries, err := os.ReadDir(paths.PlanDir())
	if err != nil {
		t.Fatalf("read plan dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("stale temp file left behind: %s", e.Name())
		}
	}
}
