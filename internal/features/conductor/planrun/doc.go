// Package planrun executes one registered plan unit in an isolated git
// worktree.
//
// Public surface:
//
//   - [Context] is the per-plan execution context with the explicit
//     ControlRoot (Springfield-owned state/evidence) and WorktreeRoot
//     (isolated checkout where the agent runs) boundary.
//   - [PlanKey] / [WorktreePath] / [BranchName] derive deterministic
//     sanitized identifiers from a plan unit.
//   - [InputDigest] hashes the plan file + project guidance so resume can
//     refuse silent reuse on drift.
//   - [Preflight] enforces the dirty/resume matrix.
//   - [Manager] creates or reuses the git worktree for one plan.
//   - [SinglePlan] runs exactly one next eligible registered plan end-to-end
//     and persists truthful state.
package planrun
