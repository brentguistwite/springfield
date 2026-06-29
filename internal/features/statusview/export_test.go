package statusview

import "springfield/internal/features/conductor"

func BuildPlanForTest(id, title string, ps *conductor.PlanState, live bool) PlanView {
	return buildPlan(id, title, ps, live)
}

// DeriveIntegrationForTest exposes deriveIntegration to the external test package.
func DeriveIntegrationForTest(ps *conductor.PlanState) IntegrationView {
	return deriveIntegration(ps)
}
