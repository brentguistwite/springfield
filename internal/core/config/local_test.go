package config

import "testing"

func TestReviewConfigMaxReviewIterationsOrDefault(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"unset defaults to 3", 0, 3},
		{"negative defaults to 3", -2, 3},
		{"explicit positive kept", 5, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ReviewConfig{MaxReviewIterations: tc.in}
			if got := r.MaxReviewIterationsOrDefault(); got != tc.want {
				t.Fatalf("MaxReviewIterationsOrDefault() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDefaultMaxReviewIterationsIsThree(t *testing.T) {
	if DefaultMaxReviewIterations != 3 {
		t.Fatalf("DefaultMaxReviewIterations = %d, want 3", DefaultMaxReviewIterations)
	}
}
