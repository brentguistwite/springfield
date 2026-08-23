package retro_test

import (
	"testing"

	"springfield/internal/features/retro"
)

// batches builds a Pattern with n distinct batch IDs, so a test can dial the
// batch-spread axis independently of the occurrence count.
func batches(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = string(rune('a' + i))
	}
	return out
}

func TestAboveThreshold_Boundaries(t *testing.T) {
	tests := []struct {
		name        string
		occurrences int
		batches     int
		want        bool
	}{
		// Occurrence axis pinned at the passing batch spread (exactly MinBatches).
		{"below occurrences, at min batches", retro.MinOccurrences - 1, retro.MinBatches, false},
		{"exactly min occurrences, at min batches", retro.MinOccurrences, retro.MinBatches, true},
		{"above occurrences, at min batches", retro.MinOccurrences + 1, retro.MinBatches, true},
		// Batch axis pinned at the passing occurrence count (exactly MinOccurrences).
		{"at min occurrences, below min batches", retro.MinOccurrences, retro.MinBatches - 1, false},
		{"at min occurrences, exactly min batches", retro.MinOccurrences, retro.MinBatches, true},
		{"at min occurrences, above min batches", retro.MinOccurrences, retro.MinBatches + 1, true},
		// Both below.
		{"both below", retro.MinOccurrences - 1, retro.MinBatches - 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := retro.Pattern{
				Key:         "iteration-cap",
				Occurrences: tt.occurrences,
				Batches:     batches(tt.batches),
			}
			if got := retro.AboveThreshold(p); got != tt.want {
				t.Errorf("AboveThreshold(occ=%d, batches=%d) = %v, want %v",
					tt.occurrences, tt.batches, got, tt.want)
			}
		})
	}
}

// Guard the exact boundary values the acceptance criteria call out, independent
// of the constants' current numeric value: exactly 3 occurrences and exactly 2
// batches is the lowest passing corner.
func TestThresholdConstants(t *testing.T) {
	if retro.MinOccurrences != 3 {
		t.Errorf("MinOccurrences = %d, want 3", retro.MinOccurrences)
	}
	if retro.MinBatches != 2 {
		t.Errorf("MinBatches = %d, want 2", retro.MinBatches)
	}
	// Exactly 3 occurrences across exactly 2 batches passes; one less on either
	// axis fails.
	if !retro.AboveThreshold(retro.Pattern{Occurrences: 3, Batches: batches(2)}) {
		t.Error("pattern at exactly (3 occurrences, 2 batches) should be above threshold")
	}
	if retro.AboveThreshold(retro.Pattern{Occurrences: 2, Batches: batches(2)}) {
		t.Error("pattern at 2 occurrences should be below threshold")
	}
	if retro.AboveThreshold(retro.Pattern{Occurrences: 3, Batches: batches(1)}) {
		t.Error("pattern spanning 1 batch should be below threshold")
	}
}

func TestFilter(t *testing.T) {
	in := []retro.Pattern{
		{Key: "keep-a", Occurrences: 5, Batches: batches(3)},
		{Key: "drop-occ", Occurrences: 2, Batches: batches(4)},
		{Key: "keep-b", Occurrences: 3, Batches: batches(2)},
		{Key: "drop-batch", Occurrences: 9, Batches: batches(1)},
	}
	got := retro.Filter(in)
	if len(got) != 2 {
		t.Fatalf("Filter kept %d patterns, want 2: %+v", len(got), got)
	}
	if got[0].Key != "keep-a" || got[1].Key != "keep-b" {
		t.Errorf("Filter preserved order wrong: got %q, %q", got[0].Key, got[1].Key)
	}
}

func TestFilter_EmptyNonNil(t *testing.T) {
	got := retro.Filter([]retro.Pattern{{Occurrences: 1, Batches: batches(1)}})
	if got == nil {
		t.Fatal("Filter returned nil for a non-nil input; want empty non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("Filter kept %d patterns, want 0", len(got))
	}
}
