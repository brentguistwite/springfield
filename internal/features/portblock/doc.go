// Package portblock deterministically assigns each slice a collision-free block
// of TCP ports so parallel slices can run servers and tests without colliding.
//
// A block is derived purely from a slice's 1-based ordinal (conductor
// PlanUnit.Order) and a configurable base port: slice N owns
// [base+(N-1)*BlockSize, base+(N-1)*BlockSize+BlockSize-1]. Because the mapping
// is a pure function of the ordinal, two concurrently running slices always get
// disjoint blocks and a single slice's block is identical across every
// iteration of a run — no shared allocator state, no coordination.
//
// Allocate is the deep-module entry point; Block.Env returns the two variables
// (SPRINGFIELD_PORT, SPRINGFIELD_PORT_RANGE) injected into every slice command —
// the agent run plus the setup and verify commands — so a script can bind to
// "$SPRINGFIELD_PORT" or carve sub-ports out of "$SPRINGFIELD_PORT_RANGE".
//
// Scope: allocation is deterministic ASSIGNMENT, not liveness probing.
// portblock never opens a socket to check whether a port is free; a port in the
// assigned block that an unrelated process already occupies is the operator's
// concern (raise [ports] base off the busy range). Probing would trade
// determinism — the property that makes blocks reproducible and merge-neutral —
// for a race against every other process on the host.
package portblock
