package execution

import (
	"errors"
	"fmt"
	"os"
	"time"

	"springfield/internal/core/config"
	"springfield/internal/core/lock"
	"springfield/internal/features/conductor"
)

// PlanInput describes a plan unit to register through Springfield's public
// execution boundary.
type PlanInput struct {
	ID         string
	Title      string
	Path       string
	Ref        string
	PlanBranch string
	Order      int
}

// Plan is the public projection of a registered plan unit.
type Plan struct {
	ID         string
	Title      string
	Path       string
	Ref        string
	PlanBranch string
	Order      int
}

// AddPlan registers a new plan unit under the project's execution config.
// The plan's path is normalized; the file must exist on disk. When no
// execution config exists yet, a minimal default config is bootstrapped from
// springfield.toml so first-time registration does not require a separate
// setup command.
func AddPlan(rootDir string, input PlanInput) (Plan, error) {
	if err := EnsureExecutionConfig(rootDir); err != nil {
		return Plan{}, err
	}
	project, err := conductor.LoadProject(rootDir)
	if err != nil {
		return Plan{}, err
	}
	unit, err := project.AddPlanUnit(toUnitInput(input))
	if err != nil {
		return Plan{}, err
	}
	if err := project.SaveConfig(); err != nil {
		return Plan{}, fmt.Errorf("save execution config: %w", err)
	}
	return fromUnit(unit), nil
}

// EnsureExecutionConfig writes a minimal default execution config when none
// exists, seeded with the project's primary agent priority from
// springfield.toml. Idempotent: existing config is reused unchanged.
func EnsureExecutionConfig(rootDir string) error {
	loaded, err := config.LoadFrom(rootDir)
	if err != nil {
		return err
	}
	priority := []string{}
	for _, id := range loaded.Config.Project.AgentPriority {
		if id != "" {
			priority = append(priority, id)
		}
	}
	tool := ""
	if len(priority) > 0 {
		tool = priority[0]
	}
	opts := conductor.SetupDefaults()
	opts.Tool = tool
	if _, err := conductor.Setup(rootDir, opts); err != nil {
		return fmt.Errorf("bootstrap execution config: %w", err)
	}
	return nil
}

// RemovePlan removes a plan unit by ID and clears any associated state.
//
// RemovePlan is repair-friendly: it loads config without validating
// plan_units invariants and persists the result with SaveConfigUnchecked, so
// a registry that contains an invalid entry (missing plan file, bad ref,
// duplicate order) can be repaired through Springfield without hand-editing
// JSON. After every removal, the registry is closer to the validating
// LoadProject contract.
func RemovePlan(rootDir, id string) error {
	project, err := conductor.LoadProjectRaw(rootDir)
	if err != nil {
		return err
	}
	if err := project.RemovePlanUnit(id); err != nil {
		return err
	}
	if err := project.SaveConfigUnchecked(); err != nil {
		return fmt.Errorf("save execution config: %w", err)
	}
	if err := project.SaveState(); err != nil {
		return fmt.Errorf("save execution state: %w", err)
	}
	return nil
}

// ReorderPlans replaces the execution order with the given ID sequence.
func ReorderPlans(rootDir string, orderedIDs []string) error {
	project, err := conductor.LoadProject(rootDir)
	if err != nil {
		return err
	}
	if err := project.ReorderPlanUnits(orderedIDs); err != nil {
		return err
	}
	if err := project.SaveConfig(); err != nil {
		return fmt.Errorf("save execution config: %w", err)
	}
	return nil
}

// ListPlans returns the configured plans in execution order. ListPlans is
// repair-friendly: invalid plan_units invariants do not prevent the user
// from inspecting the registry to decide what to remove.
func ListPlans(rootDir string) ([]Plan, error) {
	project, err := conductor.LoadProjectRaw(rootDir)
	if err != nil {
		return nil, err
	}
	ids := conductor.OrderedPlanUnitIDs(project.Config.PlanUnits)
	out := make([]Plan, 0, len(ids))
	for _, id := range ids {
		if u, ok := project.PlanUnitByID(id); ok {
			out = append(out, fromUnit(u))
		}
	}
	return out, nil
}

// PlanStatus is the public projection of a plan unit's runtime state.
type PlanStatus struct {
	Plan         Plan
	Status       string
	Error        string
	Agent        string
	EvidencePath string
	Attempts     int
	WorktreePath string
	Branch       string
	BaseRef      string
	BaseHead     string
	ExitReason   string
}

// RegistryStatus is the public plan-registry surface for `springfield status`
// when no batch runtime state is present.
type RegistryStatus struct {
	HasConfig bool
	Plans     []PlanStatus
	Completed int
	Total     int
	NextStep  string
}

// LoadRegistryStatus computes the plan-registry status from disk. When no
// execution config exists yet, a no-config status is returned with a next-step
// hint pointing at the registration flow rather than a failed read error.
func LoadRegistryStatus(rootDir string) (*RegistryStatus, error) {
	rs, err := loadConductorRegistryStatus(rootDir)
	if err != nil {
		return nil, err
	}
	out := &RegistryStatus{
		HasConfig: rs.HasConfig,
		Completed: rs.Completed,
		Total:     rs.Total,
		NextStep:  rs.NextStep,
	}
	for _, u := range rs.Units {
		out.Plans = append(out.Plans, PlanStatus{
			Plan:         fromUnit(u.Unit),
			Status:       string(u.Status),
			Error:        u.Error,
			Agent:        u.Agent,
			EvidencePath: u.EvidencePath,
			Attempts:     u.Attempts,
			WorktreePath: u.WorktreePath,
			Branch:       u.Branch,
			BaseRef:      u.BaseRef,
			BaseHead:     u.BaseHead,
			ExitReason:   u.ExitReason,
		})
	}
	return out, nil
}

// RenderRegistryStatus returns a human-readable plan-registry status block.
// When no execution config exists yet, the no-config registration hint is
// rendered instead of a read error.
func RenderRegistryStatus(rootDir string) (string, error) {
	rs, err := loadConductorRegistryStatus(rootDir)
	if err != nil {
		return "", err
	}
	return rs.Render(), nil
}

func loadConductorRegistryStatus(rootDir string) (*conductor.RegistryStatus, error) {
	if held := lock.Inspect(rootDir); held != nil {
		return loadRegistryStatusWhileRunning(rootDir, held)
	}

	project, err := conductor.LoadProject(rootDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return conductor.BuildRegistryStatus(nil), nil
		}
		return nil, err
	}
	if changed := project.NormalizeStaleRunning(time.Now()); len(changed) > 0 {
		if err := project.SaveState(); err != nil {
			return nil, err
		}
	}
	return conductor.BuildRegistryStatus(project), nil
}

func loadRegistryStatusWhileRunning(rootDir string, held *lock.ErrLockHeld) (*conductor.RegistryStatus, error) {
	project, err := conductor.LoadProject(rootDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return runningRegistryStatus(nil, held), nil
		}
		// A live start may be replacing state/config bytes in place.
		// Status stays truthful by falling back to a generic live-run
		// surface instead of failing on a transient partial read.
		return runningRegistryStatus(nil, held), nil
	}
	return runningRegistryStatus(project, held), nil
}

func runningRegistryStatus(project *conductor.Project, held *lock.ErrLockHeld) *conductor.RegistryStatus {
	status := conductor.BuildRegistryStatus(project)
	if held != nil && held.PID != 0 {
		status.NextStep = fmt.Sprintf(
			"Another springfield start is already running (pid %d since %s). Wait for it to finish, then re-run \"springfield status\".",
			held.PID,
			held.Since.Format(time.RFC3339),
		)
	} else {
		status.NextStep = "Another springfield start is already running. Wait for it to finish, then re-run \"springfield status\"."
	}
	return status
}

func toUnitInput(input PlanInput) conductor.PlanUnitInput {
	return conductor.PlanUnitInput{
		ID:         input.ID,
		Title:      input.Title,
		Path:       input.Path,
		Ref:        input.Ref,
		PlanBranch: input.PlanBranch,
		Order:      input.Order,
	}
}

func fromUnit(u conductor.PlanUnit) Plan {
	return Plan{
		ID:         u.ID,
		Title:      u.Title,
		Path:       u.Path,
		Ref:        u.Ref,
		PlanBranch: u.PlanBranch,
		Order:      u.Order,
	}
}
