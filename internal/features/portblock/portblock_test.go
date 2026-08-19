package portblock_test

import (
	"testing"

	"springfield/internal/features/portblock"
)

// TestAllocateFirstSliceStartsAtBase pins the mapping so slice ordinal 1 (the
// first PlanUnit.Order) gets the base port itself, not base+BlockSize.
func TestAllocateFirstSliceStartsAtBase(t *testing.T) {
	b := portblock.Allocate(42000, 1)
	if b.First != 42000 {
		t.Fatalf("First = %d, want 42000", b.First)
	}
	if b.Last != 42009 {
		t.Fatalf("Last = %d, want 42009 (block of %d)", b.Last, portblock.BlockSize)
	}
}

// TestConcurrentSlicesGetDisjointBlocks is the core parallel-safety guarantee:
// two slices running at the same time must never share a port.
func TestConcurrentSlicesGetDisjointBlocks(t *testing.T) {
	base := 42000
	seen := map[int]int{} // port -> ordinal that owns it
	for ordinal := 1; ordinal <= 25; ordinal++ {
		b := portblock.Allocate(base, ordinal)
		if b.Last-b.First+1 != portblock.BlockSize {
			t.Fatalf("ordinal %d: block width %d, want %d", ordinal, b.Last-b.First+1, portblock.BlockSize)
		}
		for p := b.First; p <= b.Last; p++ {
			if owner, clash := seen[p]; clash {
				t.Fatalf("port %d assigned to both ordinal %d and %d — blocks not disjoint", p, owner, ordinal)
			}
			seen[p] = ordinal
		}
	}
}

// TestBlockStableAcrossIterations proves a slice's block does not drift: the
// same ordinal always yields the identical block, so every iteration of a run
// hands the agent the same ports.
func TestBlockStableAcrossIterations(t *testing.T) {
	first := portblock.Allocate(42000, 7)
	for i := 0; i < 5; i++ {
		again := portblock.Allocate(42000, 7)
		if again != first {
			t.Fatalf("iteration %d: block %+v drifted from %+v", i, again, first)
		}
	}
}

// TestAllocateHonorsConfiguredBase proves the base is the anchor, so an
// operator can shift the whole scheme off a busy default range.
func TestAllocateHonorsConfiguredBase(t *testing.T) {
	b := portblock.Allocate(50000, 3)
	if b.First != 50020 || b.Last != 50029 {
		t.Fatalf("Allocate(50000, 3) = %+v, want {50020 50029}", b)
	}
}

// TestAllocateClampsNonPositiveOrdinal defends the math against a legacy plan
// unit with Order==0: it must not produce a block below base.
func TestAllocateClampsNonPositiveOrdinal(t *testing.T) {
	for _, ord := range []int{0, -1, -100} {
		b := portblock.Allocate(42000, ord)
		if b.First != 42000 || b.Last != 42009 {
			t.Fatalf("Allocate(42000, %d) = %+v, want the first block {42000 42009}", ord, b)
		}
	}
}

// TestEnvExportsBothVars pins the exact env-var names and shape every slice
// command (agent, setup, verify) receives.
func TestEnvExportsBothVars(t *testing.T) {
	env := portblock.Allocate(42000, 2).Env()
	if got := env[portblock.EnvPort]; got != "42010" {
		t.Fatalf("%s = %q, want 42010", portblock.EnvPort, got)
	}
	if got := env[portblock.EnvPortRange]; got != "42010-42019" {
		t.Fatalf("%s = %q, want 42010-42019", portblock.EnvPortRange, got)
	}
	if len(env) != 2 {
		t.Fatalf("Env() has %d entries, want exactly 2", len(env))
	}
}
