package portblock

import "strconv"

// Env var names exported into every slice command (the agent run plus the setup
// and verify commands). EnvPort is the first port of the slice's block — the
// obvious "bind here" default; EnvPortRange is the full "start-end" span for a
// slice that needs several ports (e.g. app + db + mock server).
const (
	EnvPort      = "SPRINGFIELD_PORT"
	EnvPortRange = "SPRINGFIELD_PORT_RANGE"
)

// DefaultBase is the first port assigned to slice ordinal 1 when [ports] base is
// unconfigured. 42000 sits in the ephemeral/registered range and well clear of
// common dev-server defaults (3000/5173/8080), so an out-of-the-box run rarely
// collides.
const DefaultBase = 42000

// BlockSize is the fixed number of contiguous ports reserved per slice. Ten is
// enough headroom for a slice to run an app plus a handful of test sidecars
// without leaking into the next slice's block.
const BlockSize = 10

// Block is one slice's reserved, contiguous port range [First, Last].
type Block struct {
	First int
	Last  int
}

// Allocate returns the deterministic port block for a slice at the given 1-based
// ordinal (conductor PlanUnit.Order), anchored at base. Slice ordinal 1 owns
// [base, base+BlockSize-1]; each later ordinal is offset by a whole BlockSize so
// blocks never overlap. A non-positive ordinal (a legacy unit with Order==0) is
// clamped to 1 so the math never produces a block below base.
func Allocate(base, ordinal int) Block {
	if ordinal < 1 {
		ordinal = 1
	}
	first := base + (ordinal-1)*BlockSize
	return Block{First: first, Last: first + BlockSize - 1}
}

// RangeString renders the block as "first-last" (the SPRINGFIELD_PORT_RANGE
// value).
func (b Block) RangeString() string {
	return strconv.Itoa(b.First) + "-" + strconv.Itoa(b.Last)
}

// Env returns the environment variables a slice's commands receive for this
// block: SPRINGFIELD_PORT (the first port) and SPRINGFIELD_PORT_RANGE
// ("first-last"). The returned map is freshly allocated so callers may mutate
// or merge it freely.
func (b Block) Env() map[string]string {
	return map[string]string{
		EnvPort:      strconv.Itoa(b.First),
		EnvPortRange: b.RangeString(),
	}
}
