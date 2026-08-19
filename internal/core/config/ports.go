package config

import "springfield/internal/features/portblock"

// PortsConfig is the [ports] block from springfield.toml. Like [setup] and
// [verify] (and unlike [review]), it is team-shareable — every teammate's
// parallel slices should carve their servers/tests from the same deterministic
// port scheme — so it belongs in the committed config. The zero value selects
// the built-in default base, so a project with no [ports] block still gets
// per-slice SPRINGFIELD_PORT/SPRINGFIELD_PORT_RANGE assignment.
//
// Allocation is deterministic assignment, not liveness probing: Springfield
// never checks whether a port in the block is already occupied by an unrelated
// process. When the default range clashes with something on the host, raise
// base off it.
type PortsConfig struct {
	// Base is the first port assigned to slice ordinal 1; each later slice is
	// offset by a whole portblock.BlockSize. <=0 (unset) → portblock.DefaultBase.
	// Resolve via BaseOrDefault().
	Base int `toml:"base"`
}

// BaseOrDefault returns the configured base port, or portblock.DefaultBase when
// the key is unset or non-positive.
func (p PortsConfig) BaseOrDefault() int {
	if p.Base <= 0 {
		return portblock.DefaultBase
	}
	return p.Base
}
