package planreview_test

import (
	"strings"
	"testing"
	"time"

	coreexec "springfield/internal/core/exec"
	"springfield/internal/features/conductor/planreview"
)

func stdoutEvent(data string) coreexec.Event {
	return coreexec.Event{Type: coreexec.EventStdout, Data: data, Time: time.Now()}
}

func stderrEvent(data string) coreexec.Event {
	return coreexec.Event{Type: coreexec.EventStderr, Data: data, Time: time.Now()}
}

func TestScanReviewVerdictEachClass(t *testing.T) {
	cases := []struct {
		marker string
		want   planreview.VerdictClass
	}{
		{"<review-verdict>pass</review-verdict>", planreview.VerdictPass},
		{"<review-verdict>revise</review-verdict>", planreview.VerdictRevise},
		{"<review-verdict>halt</review-verdict>", planreview.VerdictHalt},
	}
	for _, tc := range cases {
		t.Run(string(tc.want), func(t *testing.T) {
			v, found := planreview.ScanReviewVerdict([]coreexec.Event{stdoutEvent(tc.marker)})
			if !found {
				t.Fatalf("expected found=true for %q", tc.marker)
			}
			if v.Class != tc.want {
				t.Fatalf("Class = %q, want %q", v.Class, tc.want)
			}
		})
	}
}

func TestScanReviewVerdictNoneFound(t *testing.T) {
	_, found := planreview.ScanReviewVerdict([]coreexec.Event{stdoutEvent("just some prose, no marker")})
	if found {
		t.Fatal("expected found=false when no verdict marker present")
	}
}

func TestScanReviewVerdictMostSevereWins(t *testing.T) {
	events := []coreexec.Event{
		stdoutEvent("<review-verdict>pass</review-verdict>"),
		stdoutEvent("<review-verdict>halt</review-verdict>"),
		stdoutEvent("<review-verdict>revise</review-verdict>"),
	}
	v, found := planreview.ScanReviewVerdict(events)
	if !found || v.Class != planreview.VerdictHalt {
		t.Fatalf("expected halt (most severe), got found=%v class=%q", found, v.Class)
	}
}

func TestScanReviewVerdictIgnoresStderr(t *testing.T) {
	_, found := planreview.ScanReviewVerdict([]coreexec.Event{
		stderrEvent("<review-verdict>pass</review-verdict>"),
	})
	if found {
		t.Fatal("verdict on stderr must be ignored")
	}
}

func TestScanReviewVerdictFindingsAreStdoutProse(t *testing.T) {
	events := []coreexec.Event{
		stdoutEvent("Finding: missing nil check in foo()."),
		stdoutEvent("<review-verdict>revise</review-verdict>"),
	}
	v, _ := planreview.ScanReviewVerdict(events)
	if want := "Finding: missing nil check in foo()."; !strings.Contains(v.Findings, want) {
		t.Fatalf("findings %q should contain %q", v.Findings, want)
	}
}
