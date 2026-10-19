package runner

import (
	"testing"
	"time"

	"github.com/atkrad/benchmarks/experiments/http2-idle/internal/protocol"
)

func heldStats(n int, requests int64) protocol.Stats {
	per := make([]int64, n)
	for i := range per {
		per[i] = requests
	}
	return protocol.Stats{
		WallTime:     time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Format(time.RFC3339Nano),
		Accepted:     int64(n),
		Open:         int64(n),
		H2Requests:   requests,
		ConnRequests: per,
		GoVersion:    "go1.test",
	}
}

func TestCheckStatsAcceptsRecheck(t *testing.T) {
	stats := heldStats(2, 4)
	stats.ConnRequests = []int64{2, 2}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := checkStats(2, stats, "go1.test", at); err != nil {
		t.Fatal(err)
	}
}

func TestCheckStatsRejects(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		name string
		n    int
		edit func(*protocol.Stats)
	}{
		{name: "extra connection", n: 1, edit: func(s *protocol.Stats) { s.Accepted = 2 }},
		{name: "missing connection", n: 2, edit: func(s *protocol.Stats) { s.Open = 1 }},
		{name: "non http2", n: 1, edit: func(s *protocol.Stats) { s.NonH2Requests = 1 }},
		{name: "write error", n: 1, edit: func(s *protocol.Stats) { s.WriteErrors = 1 }},
		{name: "replaced request", n: 1, edit: func(s *protocol.Stats) { s.ConnRequests = []int64{1} }},
		{name: "wrong compiler", n: 0, edit: func(s *protocol.Stats) { s.GoVersion = "go1.other" }},
		{name: "stale", n: 0, edit: func(s *protocol.Stats) { s.WallTime = at.Add(-time.Minute).Format(time.RFC3339Nano) }},
		{name: "closed", n: 0, edit: func(s *protocol.Stats) { s.Closed = 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stats := heldStats(tc.n, int64(tc.n*2))
			if tc.n == 0 {
				stats.ConnRequests = nil
				stats.H2Requests = 0
			}
			tc.edit(&stats)
			if err := checkStats(tc.n, stats, "go1.test", at); err == nil {
				t.Fatal("expected a rejected measurement")
			}
		})
	}
}

func TestCheckHeld(t *testing.T) {
	stats := heldStats(3, 3)
	if err := checkHeld(3, stats); err != nil {
		t.Fatal(err)
	}
	stats.H2Requests = 6
	if err := checkHeld(3, stats); err == nil {
		t.Fatal("recheck totals must not pass the pre-recheck check")
	}
}
