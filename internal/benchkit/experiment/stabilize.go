package experiment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atkrad/benchmarks/internal/benchkit/measurement"
)

// ErrUnstable means the series did not meet the criteria before the deadline.
var ErrUnstable = errors.New("not stable")

// Observation is one poll. Stabilize overwrites At with its own clock.
type Observation struct {
	At          time.Time
	Connections int
	Goroutines  int
	RSSBytes    uint64
}

// Criteria controls how long a series must stay quiet.
// RSS is steady when its span is at most the larger of DriftBytes and
// DriftPercent of the window's minimum RSS. Goroutine counts must be equal
// across MinPolls consecutive polls. A change in Connections clears the window.
type Criteria struct {
	MinPolls     int
	Interval     time.Duration
	Timeout      time.Duration
	DriftPercent float64
	DriftBytes   uint64
}

// Outcome keeps every poll, including ones that reset the window.
type Outcome struct {
	Stable  bool
	History []Observation
	Reason  string
}

// Stabilize polls until the last MinPolls observations are steady, the
// deadline passes, or ctx is cancelled. now and sleep are optional; nil
// uses the wall clock. sleep should return ctx.Err() when ctx is cancelled.
func Stabilize(
	ctx context.Context,
	poll func(context.Context) (Observation, error),
	criteria Criteria,
	now func() time.Time,
	sleep func(context.Context, time.Duration) error,
) (Outcome, error) {
	if poll == nil {
		return Outcome{}, errors.New("stabilize: poll is nil")
	}
	if criteria.MinPolls < 1 {
		return Outcome{}, fmt.Errorf("stabilize: min polls must be >= 1")
	}
	if criteria.Interval <= 0 || criteria.Timeout <= 0 {
		return Outcome{}, fmt.Errorf("stabilize: interval and timeout must be positive")
	}
	if criteria.DriftPercent < 0 {
		return Outcome{}, fmt.Errorf("stabilize: drift percent must be >= 0")
	}
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = sleepCtx
	}
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}

	deadline := now().Add(criteria.Timeout)
	var history []Observation
	var window []Observation
	for {
		if !now().Before(deadline) {
			return Outcome{History: history, Reason: "deadline exceeded"}, ErrUnstable
		}
		obs, err := poll(ctx)
		if err != nil {
			return Outcome{History: history}, err
		}
		obs.At = now()
		history = append(history, obs)

		if len(window) > 0 && obs.Connections != window[len(window)-1].Connections {
			window = nil
		}
		window = append(window, obs)
		if len(window) > criteria.MinPolls {
			window = window[len(window)-criteria.MinPolls:]
		}
		if len(window) == criteria.MinPolls && steady(window, criteria) {
			return Outcome{Stable: true, History: history}, nil
		}
		if err := sleep(ctx, criteria.Interval); err != nil {
			return Outcome{History: history}, err
		}
	}
}

func steady(window []Observation, criteria Criteria) bool {
	connections := window[0].Connections
	goroutines := window[0].Goroutines
	samples := make([]measurement.Sample, len(window))
	for i, obs := range window {
		if obs.Connections != connections || obs.Goroutines != goroutines {
			return false
		}
		samples[i] = measurement.Sample{
			Time: obs.At,
			RSS:  measurement.Quantity{Bytes: obs.RSSBytes, Supported: true},
		}
	}
	delta, err := measurement.Drift(samples)
	if err != nil {
		return false
	}
	return delta <= allowance(window, criteria)
}

func allowance(window []Observation, criteria Criteria) uint64 {
	min := window[0].RSSBytes
	for _, obs := range window[1:] {
		if obs.RSSBytes < min {
			min = obs.RSSBytes
		}
	}
	allowed := criteria.DriftBytes
	if min > 0 && criteria.DriftPercent > 0 {
		byPercent := uint64(float64(min) * criteria.DriftPercent / 100)
		if byPercent > allowed {
			allowed = byPercent
		}
	}
	return allowed
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
