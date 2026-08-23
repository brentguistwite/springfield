package retro

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"springfield/internal/features/batch"
	"springfield/internal/features/cost"
)

// Extract reconstructs a [Report] from a finished batch's archive directory.
//
// batchDir is the per-batch archive root — in production
// .springfield/archive/<batchID> — holding archive.json (the batch.ArchiveEntry
// written by FinalizeBatch) and plans/<key>/ evidence trees relocated from
// execution/plans/<key>. Extract reads the header from archive.json, then joins
// each listed plan with its evidence tail.
//
// Tolerance is the whole point: relocation is best-effort and any single file
// may be missing, truncated, or corrupt on a crashed or partially-archived
// batch. Extract never treats that as fatal. A missing or unparseable
// archive.json yields a degraded-but-valid header (batch ID recovered from the
// directory name); an unreadable evidence file is skipped with a note in
// Report.Degraded. The returned error is reserved for a caller mistake (an empty
// batchDir), not for anything found on disk — a post-mortem tool must always get
// *some* report back.
func Extract(batchDir string) (*Report, error) {
	if strings.TrimSpace(batchDir) == "" {
		return nil, fmt.Errorf("retro: batchDir must not be empty")
	}

	r := &Report{}

	// Header. A read/parse failure is degraded, not fatal: recover the batch ID
	// from the directory name so downstream keying (which is by batch ID) still
	// works, and press on to whatever evidence survives.
	var entry batch.ArchiveEntry
	switch readArchiveEntry(filepath.Join(batchDir, archiveFileName), &entry) {
	case archiveOK:
		r.BatchID = entry.BatchID
		r.Title = entry.Title
		r.ArchivedAt = entry.ArchivedAt
		r.Reason = entry.Reason
		r.BatchMode = entry.BatchMode
		r.TotalUSD = entry.TotalUSD
	case archiveMissing:
		r.BatchID = filepath.Base(batchDir)
		r.Degraded = append(r.Degraded, "archive.json missing")
	case archiveCorrupt:
		r.BatchID = filepath.Base(batchDir)
		r.Degraded = append(r.Degraded, "archive.json corrupt")
	}

	// One PlanRetro per archived plan record, in archive order (deterministic).
	// With no archive entry there is nothing authoritative to enumerate plans
	// from, so the report is header-only — evidence dirs alone can't be trusted
	// to name plans (a leaked reused-id dir would masquerade as a batch plan).
	for _, p := range entry.Plans {
		pr := PlanRetro{ID: p.ID, Title: p.Title, Status: p.Status, Branch: p.Branch, BaseRef: p.BaseRef}
		r.extractPlanEvidence(batchDir, p, &pr)
		r.Plans = append(r.Plans, pr)
	}

	return r, nil
}

const archiveFileName = "archive.json"

type archiveReadResult int

const (
	archiveOK archiveReadResult = iota
	archiveMissing
	archiveCorrupt
)

// readArchiveEntry decodes archive.json into entry, distinguishing "not there"
// (a batch archived before evidence landed, or a wrong dir) from "there but
// unparseable" (a truncated/partial write) so the caller can note each honestly.
func readArchiveEntry(path string, entry *batch.ArchiveEntry) archiveReadResult {
	data, err := os.ReadFile(path)
	if err != nil {
		return archiveMissing
	}
	if err := json.Unmarshal(data, entry); err != nil {
		return archiveCorrupt
	}
	return archiveOK
}

// extractPlanEvidence locates the plan's evidence directory (relocated archive
// copy first, execution/plans fallback second) and folds its tail into pr. Every
// file it touches is optional: a gap is recorded in the report's Degraded list,
// never returned as an error.
func (r *Report) extractPlanEvidence(batchDir string, p batch.ArchivePlan, pr *PlanRetro) {
	evDir, source := locateEvidence(batchDir, p)
	if evDir == "" {
		pr.EvidenceMissing = true
		r.degradef("plan %s: evidence missing", p.ID)
		return
	}
	pr.EvidenceSource = source

	// summary.json — the plan-level terminal record, written once at loop exit.
	var sum onDiskSummary
	if readJSONFile(filepath.Join(evDir, "summary.json"), &sum) {
		pr.IterationCount = sum.IterationCount
		pr.TerminalStatus = sum.TerminalStatus
		pr.ExitReason = sum.ExitReason
	} else {
		r.degradef("plan %s: summary.json unreadable", p.ID)
	}

	pr.Iterations = r.readIterations(evDir, p.ID)
	pr.Stalls = countStallRecords(filepath.Join(evDir, "stalls.jsonl"))
	pr.VerifyRounds = readVerifyRounds(evDir)
}

// locateEvidence resolves the plan's evidence directory. The relocated archive
// copy (batchDir/plans/<key>) is authoritative; the live execution copy is the
// fallback for a batch whose best-effort relocation was skipped (the archive
// entry warned out) and thus left evidence in place. Returns "" when neither
// exists — the plan produced no evidence, or it was reaped.
func locateEvidence(batchDir string, p batch.ArchivePlan) (dir, source string) {
	key := evidenceKey(p)
	if key == "" {
		return "", ""
	}
	if archived := filepath.Join(batchDir, "plans", key); isDir(archived) {
		return archived, "archive"
	}
	if root := controlRoot(batchDir); root != "" {
		if live := filepath.Join(root, "execution", "plans", key, "evidence"); isDir(live) {
			return live, "execution"
		}
	}
	return "", ""
}

// evidenceKey is the path-safe plan key evidence dirs are named by. The archive
// record's EvidencePath (when set) is authoritative — its final segment is the
// exact key finalize wrote — so prefer it and fall back to sanitizing the raw
// plan ID the same way finalize does.
func evidenceKey(p batch.ArchivePlan) string {
	if p.EvidencePath != "" {
		if base := filepath.Base(p.EvidencePath); base != "" && base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	return batch.SanitizeID(p.ID)
}

// controlRoot returns the .springfield directory that batchDir lives under, so
// the execution/plans fallback resolves against the same control root regardless
// of how deeply the batch id nests. Empty when batchDir is not under a
// .springfield tree (a bare fixture dir with no fallback to offer).
func controlRoot(batchDir string) string {
	dir := filepath.Clean(batchDir)
	for {
		if filepath.Base(dir) == ".springfield" {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// readIterations walks the iter-N directories under evDir in ascending N order.
// It skips verify-iter-* (a different tail) and any dir whose suffix is not a
// number. Each iteration's flat meta.json/cost.json is the winning agent's
// record; iter-N/<agent>/ subdirs, when present, are the ordered fallback chain.
func (r *Report) readIterations(evDir, planID string) []IterationRetro {
	ents, err := os.ReadDir(evDir)
	if err != nil {
		return nil
	}
	var iters []IterationRetro
	for _, ent := range ents {
		if !ent.IsDir() {
			continue
		}
		idx, ok := iterIndex(ent.Name())
		if !ok {
			continue
		}
		iterDir := filepath.Join(evDir, ent.Name())
		it := IterationRetro{Index: idx}

		var meta onDiskMeta
		if readJSONFile(filepath.Join(iterDir, "meta.json"), &meta) {
			it.AgentID = meta.AgentID
			it.Model = meta.Model
			it.ExitCode = meta.ExitCode
			it.Classification = meta.Classification
			it.Error = meta.Error
		} else {
			r.degradef("plan %s: iter-%d meta.json unreadable", planID, idx)
		}

		var capt cost.Capture
		if readJSONFile(filepath.Join(iterDir, "cost.json"), &capt) {
			it.Adapter = capt.Adapter
			it.CostUSD = capt.CostUSD
		}

		it.Attempts = readAttemptChain(iterDir)
		iters = append(iters, it)
	}
	sort.Slice(iters, func(i, j int) bool { return iters[i].Index < iters[j].Index })
	return iters
}

// readAttemptChain returns the agent ids of the per-agent subdirs an iteration
// wrote when it fell through multiple agents, ordered by each attempt's
// started_at so the fallback chain reads in dispatch order (claude → codex). A
// single-attempt iteration writes no such subdirs and yields nil.
func readAttemptChain(iterDir string) []string {
	ents, err := os.ReadDir(iterDir)
	if err != nil {
		return nil
	}
	type attempt struct {
		agent   string
		started time.Time
	}
	var attempts []attempt
	for _, ent := range ents {
		if !ent.IsDir() {
			continue
		}
		var meta onDiskMeta
		if !readJSONFile(filepath.Join(iterDir, ent.Name(), "meta.json"), &meta) {
			continue // not an attempt subdir (or unreadable) — skip
		}
		attempts = append(attempts, attempt{agent: ent.Name(), started: meta.StartedAt})
	}
	// Sort by started_at, tie-broken by agent name so the order is total and
	// stable even when two attempts share a timestamp (or both are zero).
	sort.Slice(attempts, func(i, j int) bool {
		if attempts[i].started.Equal(attempts[j].started) {
			return attempts[i].agent < attempts[j].agent
		}
		return attempts[i].started.Before(attempts[j].started)
	})
	if len(attempts) == 0 {
		return nil
	}
	agents := make([]string, len(attempts))
	for i, a := range attempts {
		agents[i] = a.agent
	}
	return agents
}

// readVerifyRounds walks verify-iter-<round> dirs in ascending round order,
// reading each round's verify.json exit code and timeout flag.
func readVerifyRounds(evDir string) []VerifyRetro {
	ents, err := os.ReadDir(evDir)
	if err != nil {
		return nil
	}
	var rounds []VerifyRetro
	for _, ent := range ents {
		if !ent.IsDir() {
			continue
		}
		round, ok := verifyRoundIndex(ent.Name())
		if !ok {
			continue
		}
		vr := VerifyRetro{Round: round}
		var meta onDiskVerify
		if readJSONFile(filepath.Join(evDir, ent.Name(), "verify.json"), &meta) {
			vr.ExitCode = meta.ExitCode
			vr.TimedOut = meta.TimedOut
		}
		rounds = append(rounds, vr)
	}
	sort.Slice(rounds, func(i, j int) bool { return rounds[i].Round < rounds[j].Round })
	return rounds
}

// countStallRecords counts the wedge records in a stalls.jsonl file (one JSON
// object per line). A missing file is zero stalls; blank lines are ignored. The
// count, not the payload, is what a stall-wedge classifier keys on.
func countStallRecords(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// iterIndex parses "iter-<n>" → n, rejecting "verify-iter-<n>" (a different
// tail) and any non-numeric suffix. The verify guard matters because HasPrefix
// alone would misread "verify-iter-3" if the scan ever changed to a suffix test.
func iterIndex(name string) (int, bool) {
	const prefix = "iter-"
	if !strings.HasPrefix(name, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(name[len(prefix):])
	if err != nil {
		return 0, false
	}
	return n, true
}

func verifyRoundIndex(name string) (int, bool) {
	const prefix = "verify-iter-"
	if !strings.HasPrefix(name, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(name[len(prefix):])
	if err != nil {
		return 0, false
	}
	return n, true
}

func (r *Report) degradef(format string, args ...any) {
	r.Degraded = append(r.Degraded, fmt.Sprintf(format, args...))
}

// readJSONFile decodes path into v, reporting ok only on a clean read+parse.
// A missing or corrupt file is a false — the tolerant path every evidence read
// funnels through.
func readJSONFile(path string, v any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, v) == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// onDiskMeta mirrors the iter-N/meta.json shape written by execution.WriteEvidence.
// Redeclared here (rather than imported) to keep retro a leaf reader of the wire
// format, the same way cost.historical mirrors the archive entry it reads.
type onDiskMeta struct {
	AgentID        string    `json:"agent_id"`
	Model          string    `json:"model"`
	ExitCode       int       `json:"exit_code"`
	Classification string    `json:"classification"`
	StartedAt      time.Time `json:"started_at"`
	Error          string    `json:"error"`
}

// onDiskSummary mirrors summary.json written once at plan-loop exit.
type onDiskSummary struct {
	IterationCount int    `json:"iteration_count"`
	TerminalStatus string `json:"terminal_status"`
	ExitReason     string `json:"exit_reason"`
}

// onDiskVerify mirrors the subset of verify-iter-<round>/verify.json retro reads.
type onDiskVerify struct {
	ExitCode int  `json:"exit_code"`
	TimedOut bool `json:"timed_out"`
}
