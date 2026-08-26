package planrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/prd"
)

// Context carries the explicit ControlRoot vs WorktreeRoot boundary plus the
// plan-specific identity (key, branch, base ref, base head). ControlRoot owns
// .springfield/ state, evidence, prompts, and guidance reads; WorktreeRoot is
// where the agent's WorkDir lands so its writes never touch the source
// checkout.
type Context struct {
	Unit         conductor.PlanUnit
	ControlRoot  string
	WorktreeRoot string
	PlanKey      string
	Branch       string
	BaseRef      string
	BaseHead     string
}

// PlanKey returns the canonical sanitized key for a plan unit. The key is
// derived from the plan unit ID via the shared batch.SanitizeID rules so it
// is filesystem and branch safe. Plan unit IDs are already validated as a
// slug subset, so this is normally a pass-through; keeping the call routes
// any future relaxation of the ID schema through the same sanitizer.
func PlanKey(unit conductor.PlanUnit) string {
	return batch.SanitizeID(unit.ID)
}

// WorktreePath resolves the absolute worktree path for a plan unit under
// controlRoot's configured worktreeBase. existing maps known plan keys to
// already-recorded worktree paths so a key that collides on disk gets a
// numeric suffix instead of overwriting a sibling plan's worktree.
//
// existing keys are PlanKey values; values are the previously-recorded
// absolute worktree paths. Pass nil when no prior plans have recorded a
// worktree path yet.
func WorktreePath(controlRoot, worktreeBase string, unit conductor.PlanUnit, existing map[string]string) (string, error) {
	if controlRoot == "" {
		return "", fmt.Errorf("control root must not be empty")
	}
	if worktreeBase == "" {
		worktreeBase = ".worktrees"
	}
	key := PlanKey(unit)
	if key == "" {
		return "", fmt.Errorf("plan key is empty for unit %q", unit.ID)
	}
	taken := make(map[string]struct{}, len(existing))
	for ownerKey, path := range existing {
		if ownerKey == key {
			continue
		}
		taken[filepath.ToSlash(path)] = struct{}{}
	}
	base := absJoin(controlRoot, worktreeBase)
	candidate := filepath.Join(base, key)
	if _, clash := taken[filepath.ToSlash(candidate)]; clash {
		for i := 2; ; i++ {
			next := filepath.Join(base, fmt.Sprintf("%s-%d", key, i))
			if _, dup := taken[filepath.ToSlash(next)]; !dup {
				return next, nil
			}
		}
	}
	return candidate, nil
}

// BranchName returns the canonical plan branch. unit.PlanBranch wins when
// set; otherwise "springfield/<key>" provides a stable, namespaced default
// that does not collide with hand-managed branches.
func BranchName(unit conductor.PlanUnit) string {
	if strings.TrimSpace(unit.PlanBranch) != "" {
		return unit.PlanBranch
	}
	return "springfield/" + PlanKey(unit)
}

// GuidanceFiles is the canonical ordered list of project guidance files
// folded into the input digest. Order matters: any change to ordering would
// invalidate every previously-recorded digest.
var GuidanceFiles = []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"}

// InputDigest hashes the plan file plus project guidance content so resume
// can detect drift. The plan file is required: a missing plan file is a
// configuration error, not drift, and must surface before any worktree
// side-effects are taken. Guidance files are optional; missing guidance
// hashes as the canonical name with an empty body so adding a
// previously-missing guidance file invalidates the prior digest. The
// output is "sha256:<hex>" so a future digest format migration can be
// detected by prefix without ambiguity.
func InputDigest(controlRoot string, unit conductor.PlanUnit) (string, error) {
	h := sha256.New()
	planAbs := filepath.Join(controlRoot, filepath.FromSlash(unit.Path))
	if err := hashPlanFile(h, "plan:"+unit.Path, planAbs, unit); err != nil {
		return "", err
	}
	files := append([]string(nil), GuidanceFiles...)
	sort.Strings(files)
	for _, name := range files {
		if err := hashOptionalFile(h, "guidance:"+name, filepath.Join(controlRoot, name)); err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// hashPlanFile routes prd.json plans through the canonical-subset path so
// runner-mutated bookkeeping (UserStory.Passes) is drift-neutral, and falls
// back to raw-bytes hashing for legacy plain-text plan files (e.g. .md).
//
// Why a subset for prd.json: MarkPassed (the only Springfield-internal writer
// of prd.json) flips UserStory.Passes from false to true as stories complete.
// If the raw file bytes feed the digest, every successful iteration silently
// invalidates the recorded digest, so a resume after partial progress fails
// preflight-input-drift even though only Springfield's own bookkeeping moved.
// Excluding Passes restores the digest's intended semantics: drift = the
// agent-facing inputs (id/title/description/criteria/priority/deps + guidance
// files) changed between attempts.
func hashPlanFile(h io.Writer, label, path string, unit conductor.PlanUnit) error {
	if filepath.Base(path) != "prd.json" {
		// Legacy plain-text plan (.md): raw bytes preserve the existing
		// digest semantics. Same behavior as the pre-fix hashRequiredFile.
		return hashRequiredFile(h, label, path, unit)
	}
	_, _ = fmt.Fprintf(h, "%s\n", label)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("plan %q file missing at %s", unit.ID, path)
		}
		return fmt.Errorf("digest %s: %w", path, err)
	}
	var p prd.PRD
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("parse plan %q for digest: %w", unit.ID, err)
	}
	// Zero out the only runner-mutated field. Every other field
	// (id/title/description/tags + per-story id/title/description/
	// acceptance_criteria/priority/deps/notes/evidence_path) still feeds the
	// digest, so genuine operator edits to the plan still trip drift.
	for i := range p.UserStories {
		p.UserStories[i].Passes = false
	}
	canonical, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("canonicalize plan %q for digest: %w", unit.ID, err)
	}
	if _, err := h.Write(canonical); err != nil {
		return fmt.Errorf("digest %s: %w", path, err)
	}
	_, _ = fmt.Fprintf(h, "\nendfile\n")
	return nil
}

func hashRequiredFile(h io.Writer, label, path string, unit conductor.PlanUnit) error {
	_, _ = fmt.Fprintf(h, "%s\n", label)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("plan %q file missing at %s", unit.ID, path)
		}
		return fmt.Errorf("digest %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("digest %s: %w", path, err)
	}
	_, _ = fmt.Fprintf(h, "\nendfile\n")
	return nil
}

func hashOptionalFile(h io.Writer, label, path string) error {
	_, _ = fmt.Fprintf(h, "%s\n", label)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			_, _ = fmt.Fprintf(h, "missing\n")
			return nil
		}
		return fmt.Errorf("digest %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("digest %s: %w", path, err)
	}
	_, _ = fmt.Fprintf(h, "\nendfile\n")
	return nil
}

func absJoin(root, rel string) string {
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	return filepath.Clean(filepath.Join(root, rel))
}
