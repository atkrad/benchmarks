package runner

import (
	"fmt"
	"time"

	"github.com/atkrad/benchmarks/experiments/http2-idle/internal/protocol"
)

// checkHeld checks the server while connections are idle, before the recheck.
// Each connection has served one request.
func checkHeld(connections int, stats protocol.Stats) error {
	if stats.Open != int64(connections) || stats.Accepted != int64(connections) {
		return fmt.Errorf("held: open %d accepted %d, want %d", stats.Open, stats.Accepted, connections)
	}
	if stats.Closed != 0 {
		return fmt.Errorf("held: %d connections closed", stats.Closed)
	}
	if stats.H2Requests != int64(connections) {
		return fmt.Errorf("held: h2 requests %d, want %d", stats.H2Requests, connections)
	}
	if stats.NonH2Requests != 0 || stats.WriteErrors != 0 {
		return fmt.Errorf("held: non-http2 %d write errors %d", stats.NonH2Requests, stats.WriteErrors)
	}
	return nil
}

// checkStats fails closed when the server's counters, protocol, compiler, or
// clock do not match the trial after the recheck. Each connection has served
// two requests, and none were replaced.
func checkStats(connections int, stats protocol.Stats, wantVersion string, sampledAt time.Time) error {
	if stats.GoVersion == "" {
		return fmt.Errorf("stats: missing go version")
	}
	if wantVersion != "" && stats.GoVersion != wantVersion {
		return fmt.Errorf("stats: go version %q, want %q", stats.GoVersion, wantVersion)
	}
	wall, err := time.Parse(time.RFC3339Nano, stats.WallTime)
	if err != nil {
		return fmt.Errorf("stats: wall time: %w", err)
	}
	if !sampledAt.IsZero() {
		skew := sampledAt.Sub(wall)
		if skew < 0 {
			skew = -skew
		}
		if skew > 5*time.Second {
			return fmt.Errorf("stats: wall time is %s away from the sample", skew)
		}
	}
	if stats.Open != int64(connections) || stats.Accepted != int64(connections) {
		return fmt.Errorf("stats: open %d accepted %d, want %d", stats.Open, stats.Accepted, connections)
	}
	if stats.Closed != 0 {
		return fmt.Errorf("stats: %d connections closed during the trial", stats.Closed)
	}
	wantRequests := int64(connections * 2)
	if stats.H2Requests != wantRequests {
		return fmt.Errorf("stats: h2 requests %d, want %d", stats.H2Requests, wantRequests)
	}
	if stats.NonH2Requests != 0 {
		return fmt.Errorf("stats: %d non-http2 requests", stats.NonH2Requests)
	}
	if stats.WriteErrors != 0 {
		return fmt.Errorf("stats: %d write errors", stats.WriteErrors)
	}
	if connections == 0 {
		if len(stats.ConnRequests) != 0 {
			return fmt.Errorf("stats: baseline has connection request counts %v", stats.ConnRequests)
		}
		return nil
	}
	if len(stats.ConnRequests) != connections {
		return fmt.Errorf("stats: %d connection counters, want %d", len(stats.ConnRequests), connections)
	}
	for _, n := range stats.ConnRequests {
		if n != 2 {
			return fmt.Errorf("stats: a connection served %d requests, want 2", n)
		}
	}
	return nil
}
