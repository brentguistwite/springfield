// Package conductor implements Springfield's internal conductor orchestration layer.
//
// Internal surface:
//
//   - [Config] and [State] model persisted conductor data.
//   - [PlanState] tracks status, timing, agent, evidence, and attempt count per plan.
//   - [Project] loads and saves conductor config/state from .springfield/.
//   - [Schedule] derives execution phases from conductor config.
//   - [Runner] executes phases via a [PlanExecutor].
//   - [Diagnose] summarizes current state and next steps for internal callers.
package conductor
