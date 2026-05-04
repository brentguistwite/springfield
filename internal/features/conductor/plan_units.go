package conductor

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	// idPattern restricts plan-unit IDs to a slug-friendly subset so they are
	// safe to embed in filenames, JSON keys, and CLI args.
	idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	// refPattern is the shared rule for plan ref / plan_branch fields. It
	// rejects whitespace, leading/trailing dashes, and characters with shell
	// or git-ref ambiguity while accepting the common branch-naming subset.
	refPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
)

// PlanUnitInput is the validated input shape for registering or updating a
// plan unit. Fields mirror PlanUnit minus computed Order.
type PlanUnitInput struct {
	ID          string
	Title       string
	Description string
	Path        string
	Ref         string
	PlanBranch  string
	Order       int
}

// OrderedPlanUnitIDs returns the IDs in ascending Order, ties broken by ID.
// Stable across calls regardless of input slice ordering.
func OrderedPlanUnitIDs(units []PlanUnit) []string {
	cp := make([]PlanUnit, len(units))
	copy(cp, units)
	sort.SliceStable(cp, func(i, j int) bool {
		if cp[i].Order != cp[j].Order {
			return cp[i].Order < cp[j].Order
		}
		return cp[i].ID < cp[j].ID
	})
	ids := make([]string, 0, len(cp))
	for _, u := range cp {
		ids = append(ids, u.ID)
	}
	return ids
}

// NormalizePlanPath canonicalizes a plan path under plansDir. It rejects
// absolute paths and any path that escapes plansDir or the project root.
//
// Accepted inputs: a bare filename (resolved under plansDir), or a project-
// relative path that lives under plansDir on disk after cleaning.
func NormalizePlanPath(plansDir, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("plan path must not be empty")
	}
	if filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("plan path must be project-relative, got absolute %q", raw)
	}

	cleanPlansDir := filepath.Clean(plansDir)
	cleaned := filepath.Clean(trimmed)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("plan path escapes project root: %q", raw)
	}

	// If the cleaned path is already under plansDir, accept it.
	if cleaned == cleanPlansDir || strings.HasPrefix(cleaned, cleanPlansDir+string(filepath.Separator)) {
		return filepath.ToSlash(cleaned), nil
	}

	// If the path has no directory component, treat it as living in plansDir.
	if !strings.ContainsRune(cleaned, filepath.Separator) {
		joined := filepath.Join(cleanPlansDir, cleaned)
		return filepath.ToSlash(joined), nil
	}

	return "", fmt.Errorf("plan path %q must live under plans_dir %q or be a bare filename", raw, plansDir)
}

// ValidateRef enforces the shared rule for ref and plan_branch fields.
// Empty input is accepted; both fields are optional.
func ValidateRef(ref string) error {
	if ref == "" {
		return nil
	}
	if !refPattern.MatchString(ref) {
		return fmt.Errorf("invalid ref %q: must match %s", ref, refPattern.String())
	}
	if strings.Contains(ref, "..") {
		return fmt.Errorf("invalid ref %q: must not contain \"..\"", ref)
	}
	return nil
}

// pseudoRefNames are git pseudo-refs that resolve to a SHA but are never
// local branches. Slice-3 merge integration must publish back to a local
// branch; pseudo-refs are rejected up front so a refused ref shape can
// never reach the merge phase.
var pseudoRefNames = map[string]struct{}{
	"HEAD":             {},
	"FETCH_HEAD":       {},
	"MERGE_HEAD":       {},
	"ORIG_HEAD":        {},
	"CHERRY_PICK_HEAD": {},
	"REVERT_HEAD":      {},
}

// ValidateLocalBranchRef extends [ValidateRef] with the slice-3 contract
// that ref/plan_branch fields must name a local branch. The slice-3 merge
// phase publishes via `git update-ref refs/heads/<ref>`, so any ref shape
// that cannot live under refs/heads/ is rejected at registration time
// rather than failing later inside the merge phase with a confusing
// CAS-loss message.
//
// Rejected shapes:
//
//   - fully qualified refs (any ref starting with "refs/")
//   - pseudo-refs (HEAD, FETCH_HEAD, MERGE_HEAD, etc.)
//   - revision modifiers ("~", "^", "@{...}")
//
// Empty input is accepted: ref defaults to the current branch at execution
// time, plan_branch defaults to springfield/<plan-key>.
func ValidateLocalBranchRef(ref string) error {
	if ref == "" {
		return nil
	}
	// Check shape-specific rejections first so the error message names the
	// real issue (fully-qualified, pseudo-ref, revision modifier) rather
	// than falling through to the generic refPattern mismatch.
	if strings.HasPrefix(ref, "refs/") {
		return fmt.Errorf("ref %q is a fully-qualified ref; use a local branch name (e.g. \"main\")", ref)
	}
	if _, isPseudo := pseudoRefNames[ref]; isPseudo {
		return fmt.Errorf("ref %q is a git pseudo-ref; use a local branch name", ref)
	}
	if strings.ContainsAny(ref, "~^") || strings.Contains(ref, "@{") {
		return fmt.Errorf("ref %q contains revision modifiers; use a plain branch name", ref)
	}
	if err := ValidateRef(ref); err != nil {
		return err
	}
	return nil
}

// ValidatePlanUnitID enforces the slug rule for plan-unit IDs.
func ValidatePlanUnitID(id string) error {
	if id == "" {
		return fmt.Errorf("plan unit id must not be empty")
	}
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid plan unit id %q: must match %s", id, idPattern.String())
	}
	return nil
}

// ValidatePlanUnit performs structural and on-disk validation for one unit.
// rootDir is the project root used to resolve the canonical plan path on
// disk. When rootDir is empty, on-disk existence is not checked.
func ValidatePlanUnit(unit PlanUnit, rootDir, plansDir string) error {
	if err := ValidatePlanUnitID(unit.ID); err != nil {
		return err
	}
	if _, err := NormalizePlanPath(plansDir, unit.Path); err != nil {
		return err
	}
	if err := ValidateLocalBranchRef(unit.Ref); err != nil {
		return fmt.Errorf("plan %q ref: %w", unit.ID, err)
	}
	if err := ValidateLocalBranchRef(unit.PlanBranch); err != nil {
		return fmt.Errorf("plan %q plan_branch: %w", unit.ID, err)
	}
	if unit.Order < 1 {
		return fmt.Errorf("plan %q order must be >= 1, got %d", unit.ID, unit.Order)
	}
	if rootDir != "" {
		full := filepath.Join(rootDir, filepath.FromSlash(unit.Path))
		info, err := os.Stat(full)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("plan %q file not found at %s", unit.ID, full)
			}
			return fmt.Errorf("plan %q stat %s: %w", unit.ID, full, err)
		}
		if info.IsDir() {
			return fmt.Errorf("plan %q path %s is a directory, not a file", unit.ID, full)
		}
	}
	return nil
}

// ValidateConfigPlanUnits checks the registry as a whole: per-unit validation
// plus collection invariants (unique IDs, unique Order values).
func ValidateConfigPlanUnits(cfg *Config, rootDir string) error {
	seenID := make(map[string]struct{}, len(cfg.PlanUnits))
	seenOrder := make(map[int]string, len(cfg.PlanUnits))
	for _, u := range cfg.PlanUnits {
		if err := ValidatePlanUnit(u, rootDir, cfg.PlansDir); err != nil {
			return err
		}
		if _, dup := seenID[u.ID]; dup {
			return fmt.Errorf("duplicate plan unit id %q", u.ID)
		}
		seenID[u.ID] = struct{}{}
		if other, dup := seenOrder[u.Order]; dup {
			return fmt.Errorf("duplicate plan unit order %d shared by %q and %q", u.Order, other, u.ID)
		}
		seenOrder[u.Order] = u.ID
	}
	return nil
}

// nextOrder returns the next available 1-based order slot.
func nextOrder(units []PlanUnit) int {
	max := 0
	for _, u := range units {
		if u.Order > max {
			max = u.Order
		}
	}
	return max + 1
}

// AddPlanUnit appends a validated plan unit to the project config.
// Order is assigned automatically when input.Order == 0.
func (p *Project) AddPlanUnit(input PlanUnitInput) (PlanUnit, error) {
	if p.Config == nil {
		return PlanUnit{}, fmt.Errorf("project config is not loaded")
	}
	if err := ValidatePlanUnitID(input.ID); err != nil {
		return PlanUnit{}, err
	}
	for _, existing := range p.Config.PlanUnits {
		if existing.ID == input.ID {
			return PlanUnit{}, fmt.Errorf("plan unit %q already exists", input.ID)
		}
	}
	canonicalPath, err := NormalizePlanPath(p.Config.PlansDir, input.Path)
	if err != nil {
		return PlanUnit{}, err
	}
	if err := ValidateLocalBranchRef(input.Ref); err != nil {
		return PlanUnit{}, err
	}
	if err := ValidateLocalBranchRef(input.PlanBranch); err != nil {
		return PlanUnit{}, err
	}
	order := input.Order
	if order == 0 {
		order = nextOrder(p.Config.PlanUnits)
	} else if order < 1 {
		return PlanUnit{}, fmt.Errorf("order must be >= 1, got %d", order)
	} else {
		for _, existing := range p.Config.PlanUnits {
			if existing.Order == order {
				return PlanUnit{}, fmt.Errorf("order %d already used by plan %q", order, existing.ID)
			}
		}
	}

	unit := PlanUnit{
		ID:          input.ID,
		Title:       strings.TrimSpace(input.Title),
		Description: strings.TrimSpace(input.Description),
		Path:        canonicalPath,
		Ref:         input.Ref,
		PlanBranch:  input.PlanBranch,
		Order:       order,
	}

	full := filepath.Join(p.runtime.RootDir, filepath.FromSlash(canonicalPath))
	if info, err := os.Stat(full); err != nil {
		if os.IsNotExist(err) {
			return PlanUnit{}, fmt.Errorf("plan file not found at %s", full)
		}
		return PlanUnit{}, fmt.Errorf("stat %s: %w", full, err)
	} else if info.IsDir() {
		return PlanUnit{}, fmt.Errorf("plan path %s is a directory, not a file", full)
	}

	p.Config.PlanUnits = append(p.Config.PlanUnits, unit)
	return unit, nil
}

// RemovePlanUnit removes a plan unit by ID and clears any associated state.
func (p *Project) RemovePlanUnit(id string) error {
	if p.Config == nil {
		return fmt.Errorf("project config is not loaded")
	}
	idx := -1
	for i, u := range p.Config.PlanUnits {
		if u.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("plan unit %q not found", id)
	}
	p.Config.PlanUnits = append(p.Config.PlanUnits[:idx], p.Config.PlanUnits[idx+1:]...)
	if p.State != nil {
		delete(p.State.Plans, id)
	}
	return nil
}

// ReorderPlanUnits sets execution order to the given ID sequence. Every
// existing plan unit ID must appear exactly once.
func (p *Project) ReorderPlanUnits(orderedIDs []string) error {
	if p.Config == nil {
		return fmt.Errorf("project config is not loaded")
	}
	if len(orderedIDs) != len(p.Config.PlanUnits) {
		return fmt.Errorf("reorder requires every plan unit id; got %d, want %d", len(orderedIDs), len(p.Config.PlanUnits))
	}
	byID := make(map[string]int, len(p.Config.PlanUnits))
	for i, u := range p.Config.PlanUnits {
		byID[u.ID] = i
	}
	seen := make(map[string]struct{}, len(orderedIDs))
	for pos, id := range orderedIDs {
		if _, ok := byID[id]; !ok {
			return fmt.Errorf("unknown plan unit id %q", id)
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("duplicate plan unit id %q in reorder list", id)
		}
		seen[id] = struct{}{}
		p.Config.PlanUnits[byID[id]].Order = pos + 1
	}
	return nil
}

// PlanUnitByID returns the plan unit with the given id, if any.
func (p *Project) PlanUnitByID(id string) (PlanUnit, bool) {
	for _, u := range p.Config.PlanUnits {
		if u.ID == id {
			return u, true
		}
	}
	return PlanUnit{}, false
}
