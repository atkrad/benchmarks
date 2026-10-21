package experiment

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.now = c.now.Add(d)
	return nil
}

func TestStabilizeSteadyWindow(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_000, 0)}
	var n int
	out, err := Stabilize(t.Context(), func(context.Context) (Observation, error) {
		n++
		return Observation{ActiveUnits: 2, Goroutines: 10, RSSBytes: 1000 + uint64(n%2)}, nil
	}, Criteria{
		ExpectedActiveUnits: 2,
		MinPolls:            5,
		Interval:            time.Second,
		Timeout:             20 * time.Second,
		DriftPercent:        1,
		DriftBytes:          10,
	}, clock.Now, clock.Sleep)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Stable || len(out.History) != 5 {
		t.Fatalf("stable=%v history=%d", out.Stable, len(out.History))
	}
	for i := 1; i < len(out.History); i++ {
		if !out.History[i].At.After(out.History[i-1].At) {
			t.Fatal("history timestamps did not increase")
		}
	}
}

func TestStabilizeResetsOnConnectionChange(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	seq := []Observation{
		{ActiveUnits: 1, Goroutines: 4, RSSBytes: 100},
		{ActiveUnits: 1, Goroutines: 4, RSSBytes: 100},
		{ActiveUnits: 2, Goroutines: 4, RSSBytes: 100},
		{ActiveUnits: 2, Goroutines: 4, RSSBytes: 100},
		{ActiveUnits: 2, Goroutines: 4, RSSBytes: 100},
	}
	var i int
	out, err := Stabilize(context.Background(), func(context.Context) (Observation, error) {
		obs := seq[i]
		i++
		return obs, nil
	}, Criteria{ExpectedActiveUnits: 2, MinPolls: 3, Interval: time.Millisecond, Timeout: time.Second}, clock.Now, clock.Sleep)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.History) != 5 {
		t.Fatalf("history = %d, want 5 (the early connection count must be kept)", len(out.History))
	}
}

func TestStabilizeGoroutineGlitch(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	// Five equal polls are required. A single different value restarts the run.
	goroutines := []int{5, 5, 6, 5, 5, 5, 5, 5}
	var i int
	out, err := Stabilize(context.Background(), func(context.Context) (Observation, error) {
		g := goroutines[i]
		i++
		return Observation{ActiveUnits: 1, Goroutines: g, RSSBytes: 50}, nil
	}, Criteria{ExpectedActiveUnits: 1, MinPolls: 5, Interval: time.Millisecond, Timeout: time.Minute}, clock.Now, clock.Sleep)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.History) != len(goroutines) {
		t.Fatalf("history = %d, want %d", len(out.History), len(goroutines))
	}
}

func TestStabilizeRSSDrift(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	rss := []uint64{100, 100, 100, 100, 140, 140, 140, 140, 140}
	var i int
	out, err := Stabilize(context.Background(), func(context.Context) (Observation, error) {
		value := rss[i]
		i++
		return Observation{ActiveUnits: 1, Goroutines: 3, RSSBytes: value}, nil
	}, Criteria{
		ExpectedActiveUnits: 1,
		MinPolls:            5,
		Interval:            time.Millisecond,
		Timeout:             time.Minute,
		DriftPercent:        10,
		DriftBytes:          10,
	}, clock.Now, clock.Sleep)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.History) != len(rss) {
		t.Fatalf("history = %d, want %d", len(out.History), len(rss))
	}
}

func TestStabilizeDeadline(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	out, err := Stabilize(context.Background(), func(context.Context) (Observation, error) {
		return Observation{ActiveUnits: 1, Goroutines: 1, RSSBytes: 1}, nil
	}, Criteria{ExpectedActiveUnits: 1, MinPolls: 50, Interval: 10 * time.Millisecond, Timeout: 30 * time.Millisecond}, clock.Now, clock.Sleep)
	if !errors.Is(err, ErrUnstable) {
		t.Fatalf("err = %v", err)
	}
	if out.Stable || len(out.History) == 0 || out.Reason == "" {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestStabilizeCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Stabilize(ctx, func(context.Context) (Observation, error) {
		t.Fatal("poll ran after cancel")
		return Observation{}, nil
	}, Criteria{ExpectedActiveUnits: 1, MinPolls: 2, Interval: time.Millisecond, Timeout: time.Second}, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestStabilizeCancelDuringSleep(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	ctx, cancel := context.WithCancel(context.Background())
	var polls int
	out, err := Stabilize(ctx, func(context.Context) (Observation, error) {
		polls++
		if polls == 1 {
			cancel()
		}
		return Observation{ActiveUnits: 1, Goroutines: 1, RSSBytes: 1}, nil
	}, Criteria{ExpectedActiveUnits: 1, MinPolls: 5, Interval: time.Second, Timeout: time.Minute}, clock.Now, func(ctx context.Context, d time.Duration) error {
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if len(out.History) != 1 {
		t.Fatalf("history = %d", len(out.History))
	}
}

func TestStabilizeRejectsOnePoll(t *testing.T) {
	_, err := Stabilize(context.Background(), func(context.Context) (Observation, error) {
		return Observation{ActiveUnits: 1}, nil
	}, Criteria{ExpectedActiveUnits: 1, MinPolls: 1, Interval: time.Millisecond, Timeout: time.Second}, time.Now, nil)
	if err == nil {
		t.Fatal("expected one poll to be rejected")
	}
}

func TestStabilizeIgnoresWrongActiveUnits(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	var n int
	out, err := Stabilize(context.Background(), func(context.Context) (Observation, error) {
		n++
		units := 9
		if n > 3 {
			units = 2
		}
		return Observation{ActiveUnits: units, Goroutines: 1, RSSBytes: 10}, nil
	}, Criteria{ExpectedActiveUnits: 2, MinPolls: 2, Interval: time.Millisecond, Timeout: time.Second}, clock.Now, clock.Sleep)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.History) != 5 {
		t.Fatalf("history = %d, want 5", len(out.History))
	}
}

func TestStabilizeRejectsBadCriteria(t *testing.T) {
	_, err := Stabilize(context.Background(), func(context.Context) (Observation, error) {
		return Observation{}, nil
	}, Criteria{}, time.Now, nil)
	if err == nil {
		t.Fatal("expected invalid criteria to fail")
	}
}
