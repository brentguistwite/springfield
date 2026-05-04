// Package planmerge integrates one successfully-executed plan branch back
// into its recorded target branch through an isolated merge worktree.
//
// Public surface:
//
//   - [Git] is the small git boundary planmerge depends on (resolve, head,
//     worktree add detached, worktree remove, fast-forward merge,
//     branch delete, atomic ref update).
//   - [CLIGit] is the system-git implementation suitable for production.
//   - [Integrate] runs the full merge lifecycle for one plan: strict
//     target-drift refusal, ff-only merge in a dedicated detached
//     worktree, atomic CAS update-ref, then the cleanup matrix.
//
// Strict policy. Integrate refuses to merge whenever the current target
// branch head no longer matches the plan's recorded base_head. The merge is
// always performed in a dedicated merge worktree separate from the source
// checkout and the execution worktree. Cleanup deletes the merge worktree,
// execution worktree, and plan branch only on a clean success path; merge
// refusal, merge failure, or cleanup failure preserve the affected
// artifacts so an operator can recover from a truthful starting point.
package planmerge
