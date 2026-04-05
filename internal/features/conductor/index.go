// Package conductor implements Springfield's conductor orchestration surface.
//
// Public surface:
//
//   - [Config] and [State] model persisted conductor data.
//   - [Project] loads and saves conductor config/state from .springfield/.
//   - [Schedule] derives execution phases from conductor config.
//   - [Runner] executes phases via a [PlanExecutor].
//   - [Diagnose] summarizes current state and next steps.
package conductor
