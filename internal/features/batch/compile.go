package batch

import (
	"encoding/json"
	"fmt"
	"strings"

	"springfield/internal/features/conductor"
	"springfield/internal/features/prd"
)

// CompileInput carries the PRD envelope and caller-provided context for one batch.
type CompileInput struct {
	Envelope    prd.BatchPRDEnvelope
	ExistingIDs map[string]struct{}
	// RegisteredPlanIDs is the set of plan unit IDs already registered in the
	// conductor config (standalone plans added via "springfield plans add").
	// These are seeded into the batch-ID uniqueness set so the batch title does
	// not collide with a standalone plan directory under .springfield/plans/<id>/.
	RegisteredPlanIDs map[string]struct{}
}

// WrittenPlan carries the serialized per-plan content produced by Compile.
// PRDBytes is the indented JSON for prd.json. ContextBytes is the raw
// context_md from the envelope (nil/empty when not provided).
type WrittenPlan struct {
	ID           string
	PRDBytes     []byte
	ContextBytes []byte
}

// CompileOutput is the result of compiling a batch from a PRD envelope.
// Plans carries per-plan serialized content (one entry per envelope plan).
// Units is the PlanUnit registrations (one per plan) ordered by first-appearance.
// Warnings carries non-fatal validation warnings (e.g. large context_md or a
// hard-to-verify acceptance criterion). cmd/plan.go prints these to stderr as
// [warn] lines.
type CompileOutput struct {
	Batch    Batch
	Source   string
	Plans    []WrittenPlan
	Units    []conductor.PlanUnit
	Warnings []string
}

// Compile turns a CompileInput (PRD envelope) into a ready-to-persist Batch.
// PlanIDs is the ordered-unique union of all plan IDs referenced across
// phases[].plans, preserving first-seen order.
//
// Compile calls prd.Validate before compiling; any hard error (phase references
// unknown plan, invalid plan ID slug, etc.) causes an immediate return. Warnings
// are collected into CompileOutput.Warnings for the caller to surface.
func Compile(in CompileInput) (CompileOutput, error) {
	env := in.Envelope

	result := prd.Validate(env)
	if result.HasErrors() {
		return CompileOutput{}, result.Errors[0]
	}

	// Seed the uniqueness set from caller-provided existing IDs, registered
	// standalone plan IDs, plus all plan IDs from the envelope. This prevents the
	// batch ID from colliding with a plan ID — both would land under
	// .springfield/plans/<id>/ and an archive/delete of the batch dir would
	// silently destroy the plan's registered files.
	batchIDSeen := make(map[string]struct{})
	for k := range in.ExistingIDs {
		batchIDSeen[k] = struct{}{}
	}
	for k := range in.RegisteredPlanIDs {
		batchIDSeen[k] = struct{}{}
	}
	for _, p := range env.Plans {
		batchIDSeen[p.ID] = struct{}{}
	}
	rawID := SanitizeID(env.Title)
	if rawID == "" {
		rawID = "batch"
	}
	batchID := UniqueID(rawID, batchIDSeen)

	// Build PlanIDs: ordered-unique union across phases[].plans, preserving first-seen order.
	seen := map[string]struct{}{}
	planIDs := make([]string, 0)
	orderByID := map[string]int{}
	order := 1
	for _, ph := range env.Phases {
		for _, id := range ph.Plans {
			if _, already := seen[id]; already {
				continue
			}
			seen[id] = struct{}{}
			planIDs = append(planIDs, id)
			orderByID[id] = order
			order++
		}
	}

	// Build index of envelope plans by ID.
	planByID := make(map[string]prd.BatchPRDPlan, len(env.Plans))
	for _, p := range env.Plans {
		planByID[p.ID] = p
	}

	// Build WrittenPlans and PlanUnits, ordered by first-appearance.
	writtenPlans := make([]WrittenPlan, 0, len(planIDs))
	units := make([]conductor.PlanUnit, 0, len(planIDs))

	for _, id := range planIDs {
		ep, ok := planByID[id]
		if !ok {
			// prd.Validate already rejected unknown plan refs; this cannot happen.
			continue
		}

		// Marshal only the PRD fields (strip context_md).
		inner := ep.PRD
		prdBytes, err := json.MarshalIndent(inner, "", "  ")
		if err != nil {
			return CompileOutput{}, fmt.Errorf("marshal prd for plan %q: %w", id, err)
		}
		prdBytes = append(prdBytes, '\n')

		var ctxBytes []byte
		if ep.ContextMD != "" {
			ctxBytes = []byte(ep.ContextMD)
		}

		writtenPlans = append(writtenPlans, WrittenPlan{
			ID:           id,
			PRDBytes:     prdBytes,
			ContextBytes: ctxBytes,
		})

		// PlanUnit: Path is the canonical project-relative path to the plan's prd.json.
		// Sibling files (context.md, progress.md) are derived via filepath.Dir(unit.Path).
		units = append(units, conductor.PlanUnit{
			ID:    id,
			Title: ep.Title,
			Path:  ".springfield/plans/" + id + "/prd.json",
			Order: orderByID[id],
		})
	}

	// Build phases from envelope phases.
	phases := make([]Phase, 0, len(env.Phases))
	for _, ph := range env.Phases {
		phases = append(phases, Phase{
			Mode:  PhaseMode(ph.Mode),
			Plans: ph.Plans,
		})
	}

	batch := Batch{
		ID:      batchID,
		Title:   strings.TrimSpace(env.Title),
		Phases:  phases,
		PlanIDs: planIDs,
	}

	return CompileOutput{
		Batch:    batch,
		Source:   env.Source,
		Plans:    writtenPlans,
		Units:    units,
		Warnings: result.Warnings,
	}, nil
}
