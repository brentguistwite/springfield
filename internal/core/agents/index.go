package agents

import (
	"context"
	"fmt"
)

type Registry struct {
	ordered []Adapter
	byID    map[ID]Adapter
}

func NewRegistry(adapters ...Adapter) Registry {
	registry := Registry{
		ordered: make([]Adapter, 0, len(adapters)),
		byID:    make(map[ID]Adapter, len(adapters)),
	}

	for _, adapter := range adapters {
		if adapter == nil {
			continue
		}

		registry.ordered = append(registry.ordered, adapter)
		registry.byID[adapter.ID()] = adapter
	}

	return registry
}

func (r Registry) Resolve(input ResolveInput) (Resolved, error) {
	target := input.ProjectDefault
	source := ResolveSourceProjectDefault

	if input.PlanOverride != "" {
		target = input.PlanOverride
		source = ResolveSourcePlanOverride
	}

	adapter, ok := r.byID[target]
	if !ok {
		return Resolved{}, fmt.Errorf("%w: %s", ErrUnsupportedAgent, target)
	}

	return Resolved{
		Adapter: adapter,
		Source:  source,
	}, nil
}

func (r Registry) DetectAll(ctx context.Context) []Detection {
	results := make([]Detection, 0, len(r.ordered))

	for _, adapter := range r.ordered {
		results = append(results, adapter.Detect(ctx))
	}

	return results
}
